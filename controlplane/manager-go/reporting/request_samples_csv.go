package reporting

import (
	"encoding/json"
	"strings"
)

func csvRequestSample(row []string, index map[string]int, node, key, label, kind string, ts, elapsed int64, success bool) RequestSample {
	get := func(name string) string {
		if at, ok := index[name]; ok && at < len(row) {
			return row[at]
		}
		return ""
	}
	optional := func(names ...string) *string {
		var empty *string
		for _, name := range names {
			if _, ok := index[name]; ok {
				value := get(name)
				if value != "" {
					return &value
				}
				if empty == nil {
					empty = &value
				}
			}
		}
		return empty
	}
	sample := RequestSample{NodeID: node, StepKey: key, Label: label, SampleKind: kind, TimestampMS: ts, ElapsedMS: elapsed, Success: success, Method: optional("method", "Method"), URL: optional("URL", "url"), RequestParams: optional("samplerData", "requestParams", "request_params"), ResponseCode: optional("responseCode", "response_code"), ResponseBody: optional("responseData", "response_body"), Assertions: []RequestAssertion{}, SourceKind: "jtl", SourceName: "results.jtl"}
	raw := strings.TrimSpace(get("assertionResults"))
	if raw != "" && raw != "null" {
		var records []struct {
			Name    string `json:"name"`
			Failure *bool  `json:"failure"`
			Error   *bool  `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(raw), &records); err == nil && records != nil {
			sample.AssertionsCaptured = true
			for _, record := range records {
				// Absent flags cannot establish a passing assertion. An explicit true
				// failure/error is still positive failure evidence on its own.
				failure := record.Failure != nil && *record.Failure
				assertionError := record.Error != nil && *record.Error
				if !failure && !assertionError && (record.Failure == nil || record.Error == nil) {
					sample.AssertionsCaptured = false
					sample.Assertions = []RequestAssertion{}
					sample.Truncated = true
					break
				}
				sample.Assertions = append(sample.Assertions, RequestAssertion{Name: record.Name, Failure: failure, Error: assertionError, Message: record.Message})
			}
		} else {
			sample.Truncated = true
		}
	}
	for _, field := range []string{"failureMessage", "assertionFailureMessage"} {
		if message := strings.TrimSpace(get(field)); message != "" {
			sample.AssertionsCaptured = true
			duplicate := false
			for _, a := range sample.Assertions {
				if a.Failure && a.Message == message {
					duplicate = true
					break
				}
			}
			if !duplicate {
				sample.Assertions = append(sample.Assertions, RequestAssertion{Name: field, Failure: true, Message: message})
			}
		}
	}
	return sample
}
