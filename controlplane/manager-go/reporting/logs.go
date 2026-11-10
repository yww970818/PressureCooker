package reporting

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var event = regexp.MustCompile(`\b(ERROR|WARN|FATAL|SEVERE|Exception)\b`)

func ParseLogs(ctx context.Context, r io.Reader, name string, redact func(string) string) ([]Issue, error) {
	issues, _, err := AnalyzeLogs(ctx, r, name, redact)
	return issues, err
}
func AnalyzeLogs(ctx context.Context, r io.Reader, name string, redact func(string) string) ([]Issue, int64, error) {
	var nonempty int64
	if redact == nil {
		redact = NewRedactor(nil)
	}
	limited := &io.LimitedReader{R: r, N: 32 << 20}
	s := bufio.NewScanner(limited)
	s.Buffer(make([]byte, 4096), 256<<10)
	out := []Issue{}
	var line int64
	for s.Scan() {
		if err := ctx.Err(); err != nil {
			return out, nonempty, err
		}
		line++
		v := s.Text()
		if strings.TrimSpace(v) != "" {
			nonempty++
		}
		m := event.FindString(v)
		if m != "" && !strings.HasPrefix(v, " ") && !strings.HasPrefix(v, "\t") {
			if len(out) >= 500 {
				return out, nonempty, fmt.Errorf("log issue limit exceeded")
			}
			out = append(out, Issue{ID: fmt.Sprintf("log-%d", len(out)+1), SourceKind: "log", SourceName: redact(name), Category: "runtime", Level: m, Label: m, Message: boundedLogText(redact(v)), Count: 1, LineFrom: line, LineTo: line})
		} else if len(out) > 0 && (strings.HasPrefix(v, " ") || strings.HasPrefix(v, "\t") || strings.HasPrefix(v, "Caused by:")) {
			p := &out[len(out)-1]
			p.LineTo = line
			if len(p.Message) < 8192 {
				p.Message = boundedLogText(p.Message + "\n" + redact(v))
			}
		}
	}
	if limited.N == 0 {
		return out, nonempty, fmt.Errorf("log size limit exceeded")
	}
	if s.Err() != nil {
		return out, nonempty, fmt.Errorf("log line too large or read failed")
	}
	return out, nonempty, nil
}

func boundedLogText(s string) string {
	if len(s) > 8192 {
		return s[:8192] + " [TRUNCATED]"
	}
	return s
}
