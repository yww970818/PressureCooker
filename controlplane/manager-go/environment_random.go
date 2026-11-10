package main

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var environmentRandomRE = regexp.MustCompile(`^\{\{\$random\.(hex|alnum|digits)\(([1-9][0-9]{0,2})\)\}\}$`)

// Generator namespaces are reserved so typos and embedded rules fail closed. A rule
// is a declaration, never an expression or a request-template substitution.
func parseEnvironmentRandomRule(value string) (string, int, error) {
	if !strings.Contains(value, "$random") && !strings.Contains(value, "$auto") {
		return "", 0, nil
	}
	if value == "{{$auto.client_ip}}" {
		return "client_ip", 0, nil
	}
	if value == "{{$auto.timestamp_ms}}" {
		return "timestamp_ms", 0, nil
	}
	if value == "{{$random.uuid}}" {
		return "uuid", 36, nil
	}
	match := environmentRandomRE.FindStringSubmatch(value)
	if len(match) > 0 {
		length, _ := strconv.Atoi(match[2])
		if length <= 256 {
			return match[1], length, nil
		}
	}
	return "", 0, api(400, "invalid_environment_rule", "取值规则须为完整的 {{$auto.client_ip}}、{{$auto.timestamp_ms}}、{{$random.uuid}}、{{$random.hex(N)}}、{{$random.alnum(N)}} 或 {{$random.digits(N)}}，N为1至256且不含前导零")
}

// Unknown complete declarations remain inert authoring data. Runtime parsing
// below remains strict, so an unsupported generator cannot reach a target.
var unknownEnvironmentRuleRE = regexp.MustCompile(`^\{\{\$(auto|random)\.([a-z][a-z0-9_]*)(?:\([0-9]+\))?\}\}$`)

func validateEnvironmentRuleDeclaration(value string) error {
	match := unknownEnvironmentRuleRE.FindStringSubmatch(value)
	if len(match) > 0 {
		known := match[1] == "auto" && (match[2] == "client_ip" || match[2] == "timestamp_ms") || match[1] == "random" && contains([]string{"uuid", "hex", "alnum", "digits"}, match[2])
		if !known {
			return nil
		}
	}
	_, _, err := parseEnvironmentRandomRule(value)
	return err
}

type environmentRuleKey struct{ name, rule string }
type environmentRuleResolver struct {
	values    map[environmentRuleKey]string
	operation environmentOperation
}

func newEnvironmentRuleResolver(snapshot Obj, properties ...Obj) environmentRuleResolver {
	resolver := environmentRuleResolver{values: map[environmentRuleKey]string{}, operation: newEnvironmentOperation("", time.Now())}
	resolver.seedSnapshot(snapshot, properties...)
	return resolver
}
func (resolver environmentRuleResolver) seedSnapshot(snapshot Obj, properties ...Obj) {
	for _, raw := range arr(snapshot["variables"]) {
		pair := obj(raw)
		if rule := str(pair["generation_rule"]); rule != "" {
			resolver.values[environmentRuleKey{str(pair["name"]), rule}] = str(pair["value"])
		}
	}
	if len(properties) > 0 {
		for name, raw := range obj(snapshot["task_property_rules"]) {
			if value, exists := properties[0][name]; exists {
				resolver.values[environmentRuleKey{name, str(raw)}] = str(value)
			}
		}
	}
}
func (resolver environmentRuleResolver) withOperation(operation environmentOperation) environmentRuleResolver {
	resolver.operation = operation
	return resolver
}

func (resolver environmentRuleResolver) resolve(variables map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(variables))
	for _, name := range sortedKeys(variables) {
		value := variables[name]
		kind, length, e := parseEnvironmentRandomRule(value)
		if e != nil {
			return nil, e
		}
		if kind != "" {
			key := environmentRuleKey{name, value}
			generated, exists := resolver.values[key]
			if !exists {
				switch kind {
				case "client_ip":
					generated = resolver.operation.clientIP
					if generated == "" {
						return nil, api(400, "environment_client_ip_unavailable", "无法获取调用方请求来源IP，请使用明确的固定值或通过受支持的HTTP入口重试")
					}
				case "timestamp_ms":
					if resolver.operation.timestampMS <= 0 {
						return nil, api(500, "environment_timestamp_unavailable", "当前毫秒时间不可用")
					}
					generated = strconv.FormatInt(resolver.operation.timestampMS, 10)
				default:
					generated, e = generateEnvironmentRandom(kind, length)
				}
				if e != nil {
					return nil, api(500, "environment_random_unavailable", "随机环境变量生成失败")
				}
				resolver.values[key] = generated
			}
			value = generated
		}
		out[name] = value
	}
	return out, nil
}

// Task properties have already overridden project rows in the snapshot. Reuse
// those values and resolve additional properties once before collection merges.
func frozenTaskEnvironmentProperties(properties, snapshot Obj, resolvers ...environmentRuleResolver) (Obj, error) {
	inherited, e := environmentVariables(snapshot)
	if e != nil {
		return nil, e
	}
	definitions := map[string]string{}
	rules := Obj{}
	for name, value := range properties {
		text, e := environmentVariableText(value)
		if e != nil {
			return nil, e
		}
		if kind, _, _ := parseEnvironmentRandomRule(text); kind != "" {
			rules[name] = text
		}
		if frozen, exists := inherited[name]; exists {
			text = frozen
		}
		definitions[name] = text
	}
	resolver := newEnvironmentRuleResolver(snapshot)
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	resolved, e := resolver.resolve(definitions)
	if e != nil {
		return nil, e
	}
	delete(snapshot, "task_property_rules")
	if len(rules) > 0 {
		snapshot["task_property_rules"] = rules
	}
	out := Obj{}
	for name, value := range resolved {
		out[name] = value
	}
	return out, nil
}

func generateEnvironmentRandom(kind string, length int) (string, error) {
	if kind == "uuid" {
		var bytes [16]byte
		if _, e := rand.Read(bytes[:]); e != nil {
			return "", e
		}
		bytes[6] = (bytes[6] & 0x0f) | 0x40
		bytes[8] = (bytes[8] & 0x3f) | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
	}
	alphabet := "0123456789"
	if kind == "hex" {
		alphabet = "0123456789abcdef"
	} else if kind == "alnum" {
		alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	}
	// Rejection sampling avoids modulo bias for the 10/62-character alphabets.
	limit := 256 - 256%len(alphabet)
	output := make([]byte, 0, length)
	var bytes [64]byte
	for len(output) < length {
		if _, e := rand.Read(bytes[:]); e != nil {
			return "", e
		}
		for _, value := range bytes {
			if int(value) < limit {
				output = append(output, alphabet[int(value)%len(alphabet)])
				if len(output) == length {
					break
				}
			}
		}
	}
	return string(output), nil
}
