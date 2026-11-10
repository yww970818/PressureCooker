package main

import (
	"sort"
	"strings"
	"unicode"
)

func normalizeRunLogText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(text, "�"), "\r\n", "\n"), "\r", "\n")
}

// Mask before selecting tail lines: a selected tail or byte-limited read may
// contain only the later lines of a multiline credential. Never use today's
// mutable environment to determine sensitivity of a frozen historical run.
func publicRunLogText(run Obj, text string, truncated bool, tail int) string {
	text = normalizeRunLogText(text)
	if truncated {
		// The bounded reader may start in the middle of a credential. Discard that
		// incomplete leading line rather than exposing an arbitrary secret suffix.
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		} else {
			text = ""
		}
	}
	candidates := map[string]bool{}
	add := func(value string) {
		if strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '*' || r == '•' || r == '●' }) != "" {
			candidates[value] = true
		}
	}
	sensitive := sensitiveRunProperties(run)
	for key, value := range obj(run["properties"]) {
		if !sensitive[key] {
			continue
		}
		normalized := normalizeRunLogText(str(value))
		add(normalized)
		for _, fragment := range strings.Split(normalized, "\n") {
			add(fragment)
		}
	}
	for _, value := range cookieAccountSecrets(obj(run["properties"])) {
		add(value)
	}
	values := make([]string, 0, len(candidates))
	for value := range candidates {
		values = append(values, value)
	}
	// Containing credentials must be replaced first; masking a short Cookie
	// first can otherwise leave the longer Cookie's private suffix visible.
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "***")
	}
	text = bearerLogRE.ReplaceAllString(text, "${1}***")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n")
}
