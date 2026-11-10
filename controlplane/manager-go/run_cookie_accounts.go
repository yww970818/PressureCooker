package main

import (
	"regexp"
	"strings"
)

const cookieAccountPrefix = "cookie_account."

var cookieAccountKeyRE = regexp.MustCompile(`^(?:cookie_account|script_identity)\.([A-Za-z][A-Za-z0-9_-]{0,39})\.([a-z][a-z0-9_-]{0,39})$`)

// Named identities are ordinary signed, encrypted JMeter properties. Selection
// belongs to the script; the platform never assigns or rotates an account.
func validateCookieAccounts(properties Obj) error {
	accounts := map[string]map[string]string{}
	for key, raw := range properties {
		if !strings.HasPrefix(key, cookieAccountPrefix) && !strings.HasPrefix(key, scriptIdentityPrefix) {
			continue
		}
		match := cookieAccountKeyRE.FindStringSubmatch(key)
		value, ok := raw.(string)
		if len(match) != 3 || !ok || (len(match) == 3 && !validIdentityField(match[2])) || strings.ContainsAny(value, "\r\n\x00") {
			return api(400, "invalid_cookie_accounts", "身份属性须为 script_identity.<名称>.<声明字段>，且值为单行文本")
		}
		if accounts[match[1]] == nil {
			accounts[match[1]] = map[string]string{}
		}
		accounts[match[1]][match[2]] = value
	}
	if len(accounts) > 20 {
		return api(400, "invalid_cookie_accounts", "认证身份最多 20 条")
	}
	// Required values depend on the selected immutable script, checked after
	// environment resolution by validateExecutionIdentities.
	return nil
}

// Also mask individual cookie values if a script logs just a session value.
// Separate properties allow existing worker capture to mask each identity.
func cookieAccountSecrets(properties Obj) []string {
	values := []string{}
	for key, raw := range properties {
		match := cookieAccountKeyRE.FindStringSubmatch(key)
		if len(match) != 3 {
			continue
		}
		value := str(raw)
		if value != "" {
			values = append(values, value)
		}
		if match[2] == "cookie" {
			for _, part := range strings.Split(value, ";") {
				if _, v, ok := strings.Cut(part, "="); ok && strings.TrimSpace(v) != "" {
					values = append(values, strings.TrimSpace(v))
				}
			}
		}
	}
	return values
}
