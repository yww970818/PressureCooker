package reporting

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var credentials = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer\s+|basic\s+)?|(?:password|passwd|pwd|token|secret|api[_-]?key|cookie|set-cookie)\s*[=:]\s*)[^\s&,;]+`)
var quotedCredentials = regexp.MustCompile(`(?i)(["'](?:password|passwd|pwd|token|secret|api[_-]?key|authorization|cookie)["']\s*:\s*["'])[^"']*(?:["']|$)`)

// Header values may contain spaces and semicolon-separated credentials. Consume
// the complete line rather than guessing which cookie names are sensitive.
var sensitiveHeader = regexp.MustCompile(`(?i)(\b(?:cookie|set-cookie|authorization)\s*[:=]\s*)[^\r\n]*`)

// JMeter may dump its global Java Properties map. Suppress the entire map:
// arbitrary property names and nested cookie/header values are not enumerable.
var propertyMap = regexp.MustCompile(`(?i)(propert(?:y|ies)[^\r\n{]*\{)[^\r\n]*`)

// Remove the entire suffix, including unnamed parameters, relative URLs and fragments.
var urlSuffix = regexp.MustCompile(`(?i)(?:[?#]|%3f|%23)[^\s"]*`)
var userinfo = regexp.MustCompile(`(?i)(https?://)[^/?#@\s]+@`)

func NewRedactor(secrets []string) func(string) string {
	values := make([]string, 0, len(secrets)*3)
	for _, s := range secrets {
		if s != "" {
			for _, value := range []string{s, url.QueryEscape(s), url.PathEscape(s)} {
				// A secret may start in the path and end in the query. Match the
				// representation that remains after URL suffixes have been masked.
				values = append(values, urlSuffix.ReplaceAllString(value, "[REDACTED]"))
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return func(s string) string {
		s = urlSuffix.ReplaceAllString(s, "[REDACTED]")
		for _, v := range values {
			if v != "" {
				s = strings.ReplaceAll(s, v, "[REDACTED]")
			}
		}
		s = propertyMap.ReplaceAllString(s, "${1}[REDACTED]}")
		s = sensitiveHeader.ReplaceAllString(s, "${1}[REDACTED]")
		s = quotedCredentials.ReplaceAllString(s, "${1}[REDACTED]\"")
		s = credentials.ReplaceAllString(s, "${1}[REDACTED]")
		s = userinfo.ReplaceAllString(s, "${1}[REDACTED]@")
		return s
	}
}
