package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Obj = map[string]any

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string                { return e.Message }
func api(status int, code, message string) error { return &APIError{status, code, message} }

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)
var sensitiveRE = regexp.MustCompile(`(?i)^script_identity\.|password|passwd|secret|token|credential|authorization|api[-_.]?key|cookie`)
var active = []string{"PREPARING", "RUNNING", "STOPPING", "RECOVERING", "UPLOADING", "CALLBACK_PENDING"}
var terminal = []string{"COMPLETED", "FAILED", "STOPPED", "INTERRUPTED"}

func contains(items []string, s string) bool {
	for _, i := range items {
		if i == s {
			return true
		}
	}
	return false
}
func str(v any) string { s, _ := v.(string); return s }
func obj(v any) Obj {
	m, _ := v.(map[string]any)
	if m == nil {
		return Obj{}
	}
	return m
}
func arr(v any) []any {
	a, _ := v.([]any)
	if a == nil {
		return []any{}
	}
	return a
}
func boolean(v any) bool { b, _ := v.(bool); return b }
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
func now() string    { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }
func epoch() float64 { return float64(time.Now().UnixNano()) / 1e9 }
func newID(prefix string) string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return prefix + hex.EncodeToString(b[:])
}
func validID(s string) error {
	if !idRE.MatchString(s) || s == "." || s == ".." {
		return api(400, "invalid_id", "Invalid identifier")
	}
	return nil
}
func decode(raw []byte) (Obj, error) {
	var m Obj
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e := d.Decode(&m)
	return m, e
}

// encoded is byte compatible with Python json.dumps(ensure_ascii=False,
// sort_keys=True,separators=(',',':')), including '<' and U+2028/U+2029.
func encoded(v any) []byte { var b bytes.Buffer; canonical(&b, v); return b.Bytes() }
func jsonString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteString(s[start:i])
				b.WriteRune(utf8.RuneError)
				start = i + 1
			}
			i += size
			continue
		}
		if c >= 32 && c != '"' && c != '\\' {
			i++
			continue
		}
		b.WriteString(s[start:i])
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			const hex = "0123456789abcdef"
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
		i++
		start = i
	}
	b.WriteString(s[start:])
	b.WriteByte('"')
}
func canonical(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case string:
		jsonString(b, x)
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			jsonString(b, k)
			b.WriteByte(':')
			canonical(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			canonical(b, e)
		}
		b.WriteByte(']')
	case []string:
		a := make([]any, len(x))
		for i, s := range x {
			a[i] = s
		}
		canonical(b, a)
	case json.Number:
		b.WriteString(string(x))
	default:
		p, e := json.Marshal(x)
		if e != nil {
			panic(e)
		}
		b.Write(p)
	}
}
func digest(raw []byte) string { v := sha256.Sum256(raw); return hex.EncodeToString(v[:]) }
func clone(v Obj) Obj {
	m, e := decode(encoded(v))
	if e != nil {
		panic(e)
	}
	return m
}
func atomicWrite(path string, raw []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func safePath(base, relative string, require bool) (string, error) {
	value, e := url.PathUnescape(relative)
	if e != nil || strings.ContainsAny(value, "\\\x00%") || filepath.IsAbs(value) {
		return "", api(400, "unsafe_path", "Invalid file path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return "", api(400, "unsafe_path", "Invalid file path")
		}
	}
	// Check base and every ancestor as well as descendants, never resolve a symlink.
	for current := base; ; current = filepath.Dir(current) {
		if info, e := os.Lstat(current); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", api(400, "unsafe_path", "Symbolic links are not served")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	target := base
	for _, part := range strings.Split(value, "/") {
		target = filepath.Join(target, part)
		if info, e := os.Lstat(target); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", api(400, "unsafe_path", "Symbolic links are not served")
		}
	}
	if require {
		info, e := os.Stat(target)
		if e != nil || !info.Mode().IsRegular() {
			return "", api(404, "not_found", "File not found")
		}
	}
	return target, nil
}
func parseInt(s string, defaultValue int) (int, error) {
	if s == "" {
		return defaultValue, nil
	}
	return strconv.Atoi(s)
}
