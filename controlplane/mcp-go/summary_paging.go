package main

import "net/url"

func summaryPageSchema(extra map[string]any) map[string]any {
	out := map[string]any{"limit": intSchema(1, 1000), "offset": intSchema(0, 1000000)}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
func summaryQuery(args map[string]any) string {
	q := url.Values{"view": {"summary"}, "limit": {"200"}, "offset": {"0"}}
	for _, key := range []string{"limit", "offset"} {
		if value, ok := args[key]; ok {
			q.Set(key, queryValue(value))
		}
	}
	return "?" + q.Encode()
}
