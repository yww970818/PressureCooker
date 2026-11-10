package reporting

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const requestRawPayloadLimit = 1 << 20
const requestSensitiveNames = `(?:[a-z0-9_.-]*(?:password|passwd|pwd|token|secret|api[_.-]?key|authorization|cookie|credential|private[_.-]?key|session[_.-]?id|csrf|xsrf)[a-z0-9_.-]*|auth|session)`

var requestFoldedHeader = regexp.MustCompile(`(?im)^([ \t]*` + requestSensitiveNames + `\s*:)[^\r\n]*(?:\r?\n[ \t]+[^\r\n]*)*`)
var requestQuotedSecret = regexp.MustCompile(`(?i)(["']` + requestSensitiveNames + `["']\s*:\s*)(?:"(?:\\.|[^"\\])*"|'[^']*'|[^,}\]\r\n]+)`)
var requestPlainSecret = regexp.MustCompile(`(?im)(^|[^a-zA-Z0-9_</])(` + requestSensitiveNames + `\s*[=:]\s*)[^\r\n&;,]+`)
var requestXMLSecret = regexp.MustCompile(`(?is)(<(?:[a-z_][a-z0-9_.-]*:)?` + requestSensitiveNames + `\b[^>]*>).*?(</(?:[a-z_][a-z0-9_.-]*:)?` + requestSensitiveNames + `\s*>)`)
var requestJSONLineStart = regexp.MustCompile(`(?m)^[ \t]*[\{\[]`)
var requestURLUserinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s"<>]+@`)

func requestSensitiveKey(key string) bool {
	key = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, part := range []string{"password", "passwd", "token", "secret", "apikey", "authorization", "cookie", "credential", "privatekey", "sessionid", "csrf", "xsrf"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "pwd" || key == "auth" || key == "session"
}
func requestBoundText(value string, limit int) (string, bool) {
	truncated := false
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
		truncated = true
	}
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		truncated = true
	}
	return strings.Clone(value), truncated
}

// Decode structured values before applying the known-secret redactor. Otherwise
// JSON escaping can hide a credential from a literal secret match.
func requestPayload(value string, redact func(string) string, depth int) (string, bool) {
	if len(value) > requestRawPayloadLimit || depth > 32 {
		return "[OMITTED: payload limit]", true
	}
	// Mask folded credential headers before isolating form lines, so a folded
	// header value such as "a=secret" cannot be mistaken for ordinary form data.
	value = requestFoldedHeader.ReplaceAllString(value, "${1}[REDACTED]")
	const queryPrefix = "Query parameters:\n"
	const bodySeparator = "\n\nSampler data:\n"
	if strings.HasPrefix(value, queryPrefix) {
		parts := strings.SplitN(strings.TrimPrefix(value, queryPrefix), bodySeparator, 2)
		query, cut := requestPayload(parts[0], redact, depth+1)
		if len(parts) == 1 {
			return queryPrefix + query, cut
		}
		body, bodyCut := requestPayload(parts[1], redact, depth+1)
		return queryPrefix + query + bodySeparator + body, cut || bodyCut
	}
	if strings.TrimSpace(value) == "[no cookies]" {
		return redact(value), false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) == nil {
		var extra any
		if decoder.Decode(&extra) == io.EOF {
			clean, truncated := requestJSONValue(decoded, redact, depth)
			var out bytes.Buffer
			encoder := json.NewEncoder(&out)
			encoder.SetEscapeHTML(false)
			if encoder.Encode(clean) == nil {
				return strings.TrimSuffix(out.String(), "\n"), truncated
			}
		}
	}
	// JMeter samplerData prefixes the body with method/POST-data lines and can
	// append a cookie marker. Traverse its JSON body just like a bare document.
	if at := requestJSONLineStart.FindStringIndex(value); at != nil {
		fragment := json.NewDecoder(strings.NewReader(value[at[0]:]))
		var decodedFragment any
		prefix, prefixCut := requestPayload(value[:at[0]], redact, depth+1)
		if fragment.Decode(&decodedFragment) != nil {
			return prefix + "[OMITTED: invalid structured payload]", true
		}
		end := at[0] + int(fragment.InputOffset())
		body, bodyCut := requestPayload(value[at[0]:end], redact, depth+1)
		suffix, suffixCut := requestPayload(value[end:], redact, depth+1)
		return prefix + body + suffix, prefixCut || bodyCut || suffixCut
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// A malformed or pre-truncated structured document cannot be safely
		// traversed. Do not leave an unterminated sensitive value's tail visible.
		return "[OMITTED: invalid structured payload]", true
	}
	// Ordinary form/query fields stay useful; sensitive fields and decoded secrets
	// are masked even when their names or values were URL encoded.
	if strings.Contains(value, "=") && !strings.ContainsAny(value, "\r\n{}") && requestFormLine(value) {
		if form, e := url.ParseQuery(value); e == nil || len(form) > 0 {
			clean := url.Values{}
			truncated := e != nil
			for key, values := range form {
				for _, v := range values {
					if requestSensitiveKey(key) {
						v = "[REDACTED]"
					} else {
						var cut bool
						v, cut = requestPayload(v, redact, depth+1)
						truncated = truncated || cut
					}
					clean.Add(redact(key), v)
				}
			}
			return clean.Encode(), truncated
		}
		return "[OMITTED: invalid form payload]", true
	}
	// JMeter prefixes sampler form data with a POST-data line and may append
	// cookie metadata. Decode form lines on their own; whole-document regexes
	// cannot recognize percent-encoded field names.
	if strings.ContainsAny(value, "\r\n") {
		for start := 0; start < len(value); {
			end := strings.IndexByte(value[start:], '\n')
			if end < 0 {
				end = len(value)
			} else {
				end += start
			}
			line := strings.TrimSpace(value[start:end])
			if requestFormLine(line) && !strings.ContainsAny(line, "{}") {
				prefix, prefixCut := requestPayload(value[:start], redact, depth+1)
				form, formCut := requestPayload(line, redact, depth+1)
				suffix, suffixCut := requestPayload(value[end:], redact, depth+1)
				return prefix + form + suffix, prefixCut || formCut || suffixCut
			}
			if end == len(value) {
				break
			}
			start = end + 1
		}
	}
	// Sampler data can contain HTTP headers or a partial JSON/XML body rather than
	// one complete JSON document. These payload-only rules leave report labels alone.
	value = requestFoldedHeader.ReplaceAllString(value, "${1}[REDACTED]")
	value = requestQuotedSecret.ReplaceAllString(value, "${1}\"[REDACTED]\"")
	value = requestXMLSecret.ReplaceAllString(value, "${1}[REDACTED]${2}")
	value = requestPlainSecret.ReplaceAllString(value, "${1}${2}[REDACTED]")
	return redact(value), false
}

func requestFormLine(value string) bool {
	at := strings.IndexByte(value, '=')
	if at <= 0 {
		return false
	}
	key := strings.TrimSpace(value[:at])
	return key != "" && !strings.ContainsAny(key, " \t:\"'<>/\\")
}
func requestJSONValue(value any, redact func(string) string, depth int) (any, bool) {
	if depth > 32 {
		return "[OMITTED: nesting limit]", true
	}
	truncated := false
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if requestSensitiveKey(key) {
				out[redact(key)] = "[REDACTED]"
				continue
			}
			clean, cut := requestJSONValue(item, redact, depth+1)
			truncated = truncated || cut
			out[redact(key)] = clean
		}
		return out, truncated
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			clean, cut := requestJSONValue(item, redact, depth+1)
			out[i] = clean
			truncated = truncated || cut
		}
		return out, truncated
	case string:
		return requestPayload(v, redact, depth+1)
	default:
		// A known credential may have been serialized as a JSON number/bool,
		// rather than a string. Preserve its type only when no masking occurs.
		encoded, err := json.Marshal(value)
		if err == nil {
			if safe := redact(string(encoded)); safe != string(encoded) {
				return safe, false
			}
		}
		return value, false
	}
}
