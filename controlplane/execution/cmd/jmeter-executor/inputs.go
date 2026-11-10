package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
)

var hostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)

func validate(t protocol.Task, c config) error {
	if t.Version != 1 || t.ControllerID != c.id || t.ClusterID != c.cluster || !protocol.ValidID(t.TaskID) || !protocol.ValidID(t.ProjectID) || !protocol.ValidID(t.AttemptID) || len(t.Workers) == 0 || len(t.Workers) > 64 || t.TimeoutSeconds < 10 || t.TimeoutSeconds > 86400 || t.Plan.Size <= 0 || t.Plan.Size > 64<<20 {
		return errors.New("invalid task envelope")
	}
	if _, e := protocol.NormalizeCallbackURL(t.CallbackURL); e != nil {
		return e
	}
	seen := map[string]bool{}
	hosts := map[string]bool{}
	agents := map[string]bool{}
	for _, w := range t.Workers {
		u, e := url.Parse(w.AgentURL)
		if e != nil || !protocol.ValidID(w.ID) || seen[w.ID] || hosts[w.RMIHost] || agents[w.AgentURL] || !hostname.MatchString(w.RMIHost) || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("invalid worker")
		}
		seen[w.ID] = true
		hosts[w.RMIHost] = true
		agents[w.AgentURL] = true
	}
	for k := range t.Properties {
		if protocol.RuntimePropertyReserved(k) {
			return errors.New("reserved runtime property")
		}
	}
	fileLimit, totalLimit := c.datasetLimit, c.taskInputLimit
	if fileLimit == 0 {
		fileLimit = 256 << 20
	}
	if totalLimit == 0 {
		totalLimit = 1 << 30
	}
	nodes := map[string]bool{}
	var total int64
	for _, d := range t.Datasets {
		delim, n := utf8.DecodeRuneInString(d.Delimiter)
		if !protocol.ValidID(d.NodeID) || nodes[d.NodeID] || len(d.Files) == 0 || len(d.Columns) == 0 || len(d.Columns) != len(d.VariableNames) || d.Distribution != "copy" && d.Distribution != "shard" || d.Delimiter == "" || n != len(d.Delimiter) || strings.ContainsRune("\x00\"\r\n", delim) || delim == utf8.RuneError {
			return errors.New("invalid dataset")
		}
		nodes[d.NodeID] = true
		for _, r := range d.Files {
			if r.Size < 1 || r.Size > fileLimit {
				return errors.New("dataset file exceeds limit")
			}
			total += r.Size
			if total > totalLimit {
				return errors.New("task inputs exceed limit")
			}
		}
	}
	return nil
}
func props(v map[string]string) []byte {
	escape := func(s string) string {
		var b strings.Builder
		for _, u := range utf16.Encode([]rune(s)) {
			switch u {
			case '\\', ' ', ':', '=', '!', '#':
				b.WriteByte('\\')
				b.WriteByte(byte(u))
			case '\t':
				b.WriteString("\\t")
			case '\n':
				b.WriteString("\\n")
			case '\r':
				b.WriteString("\\r")
			case '\f':
				b.WriteString("\\f")
			default:
				if u < 32 || u > 126 {
					fmt.Fprintf(&b, "\\u%04X", u)
				} else {
					b.WriteByte(byte(u))
				}
			}
		}
		return b.String()
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(escape(k) + "=" + escape(v[k]) + "\n")
	}
	return []byte(b.String())
}

type span struct {
	from, to int
	value    string
}

// Use offsets into the original XML to preserve every unrelated byte, including
// imported extension properties, whitespace and CDATA.
func patchPlan(raw []byte, paths map[string]string) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth, level := 0, 0
	var marker string
	var filename *span
	var edits []span
	seen := map[string]bool{}
	for {
		before := int(dec.InputOffset())
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch v := tok.(type) {
		case xml.StartElement:
			depth++
			if v.Name.Local == "CSVDataSet" {
				if level != 0 {
					return nil, errors.New("nested CSV nodes")
				}
				level = depth
				marker = ""
				filename = nil
				continue
			}
			if level == 0 || depth != level+1 || v.Name.Local != "stringProp" {
				continue
			}
			name := ""
			for _, a := range v.Attr {
				if a.Name.Local == "name" {
					name = a.Value
				}
			}
			if name != "filename" && name != "jmeter.platform.node_id" {
				continue
			}
			var text string
			if e = dec.DecodeElement(&text, &v); e != nil {
				return nil, e
			}
			depth--
			if name == "jmeter.platform.node_id" {
				if marker != "" || !protocol.ValidID(text) {
					return nil, errors.New("invalid dataset marker")
				}
				marker = text
			} else {
				if filename != nil {
					return nil, errors.New("duplicate CSV filename")
				}
				filename = &span{from: before, to: int(dec.InputOffset())}
			}
		case xml.EndElement:
			if level != 0 && depth == level {
				if marker != "" {
					p, ok := paths[marker]
					if !ok || seen[marker] || filename == nil {
						return nil, errors.New("unknown or invalid CSV marker")
					}
					seen[marker] = true
					var b bytes.Buffer
					b.WriteString(`<stringProp name="filename">`)
					xml.EscapeText(&b, []byte(p))
					b.WriteString("</stringProp>")
					filename.value = b.String()
					edits = append(edits, *filename)
				}
				level = 0
			}
			depth--
		}
	}
	if len(seen) != len(paths) {
		return nil, errors.New("missing CSV node marker")
	}
	var b bytes.Buffer
	last := 0
	for _, s := range edits {
		b.Write(raw[last:s.from])
		b.WriteString(s.value)
		last = s.to
	}
	b.Write(raw[last:])
	return b.Bytes(), nil
}

type binding struct {
	NodeID string             `json:"node_id"`
	Ref    protocol.ObjectRef `json:"ref"`
}
type manifest map[string][]binding

func (s *service) taskDir(id string) string { return filepath.Join(s.c.work, "tasks", id) }
func (s *service) csvPath(task, node string) string {
	return filepath.Join(s.taskDir(task), "datasets", node, "data.csv")
}
func (s *service) prepareInputs(ctx context.Context, t protocol.Task, lease string) (manifest, []protocol.ObjectRef, error) {
	properties, err := controllerRuntimeProperties(t)
	if err != nil {
		return nil, nil, err
	}
	base := s.taskDir(t.TaskID)
	if e := directory(base); e != nil {
		return nil, nil, e
	}
	plan := filepath.Join(base, "plan.jmx")
	if e := s.s.Download(ctx, t.Plan, plan); e != nil {
		return nil, nil, e
	}
	paths := map[string]string{}
	out := manifest{}
	var refs []protocol.ObjectRef
	for _, d := range t.Datasets {
		paths[d.NodeID] = s.csvPath(t.TaskID, d.NodeID)
		parts, e := s.merge(ctx, t, d)
		if e != nil {
			return nil, nil, e
		}
		for i, p := range parts {
			label := "copy"
			if d.Distribution == "shard" {
				label = t.Workers[i].ID
			}
			key := strings.Join([]string{"task-inputs", t.ProjectID, t.TaskID, d.NodeID, label + ".csv"}, "/")
			ref, e := s.putFile(ctx, key, p, "text/csv")
			if e != nil {
				return nil, nil, e
			}
			refs = append(refs, ref)
			b := binding{d.NodeID, ref}
			if d.Distribution == "copy" {
				for _, w := range t.Workers {
					out[w.ID] = append(out[w.ID], b)
				}
			} else {
				w := t.Workers[i]
				out[w.ID] = append(out[w.ID], b)
			}
		}
	}
	raw, e := os.ReadFile(plan)
	if e != nil {
		return nil, nil, e
	}
	b, e := patchPlan(raw, paths)
	if e != nil {
		return nil, nil, e
	}
	b, e = injectRequestCapture(b, t, evidenceOwnerKey(assignment{LeaseToken: lease}))
	if e != nil {
		return nil, nil, e
	}
	b, e = injectThreadObservations(b, t, evidenceOwnerKey(assignment{LeaseToken: lease}))
	if e != nil {
		return nil, nil, e
	}
	if e = writeFile(plan, b); e != nil {
		return nil, nil, e
	}
	return out, refs, writeFile(filepath.Join(base, "task.properties"), props(properties))
}
func (s *service) merge(ctx context.Context, t protocol.Task, d protocol.DatasetInput) ([]string, error) {
	n := 1
	if d.Distribution == "shard" {
		n = len(t.Workers)
	}
	dir := filepath.Dir(s.csvPath(t.TaskID, d.NodeID))
	if e := directory(dir); e != nil {
		return nil, e
	}
	paths := make([]string, n)
	writers := make([]*bufio.Writer, n)
	files := make([]*os.File, 0, n)
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	delim, _ := utf8.DecodeRuneInString(d.Delimiter)
	for i := 0; i < n; i++ {
		paths[i] = filepath.Join(dir, fmt.Sprintf("part-%d.csv", i))
		if i == 0 {
			paths[i] = s.csvPath(t.TaskID, d.NodeID)
		}
		f, e := os.OpenFile(paths[i], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		files = append(files, f)
		writers[i] = bufio.NewWriter(f)
		header := csv.NewWriter(writers[i])
		header.Comma = delim
		if e = header.Write(d.Columns); e != nil {
			return nil, e
		}
		header.Flush()
		if e = header.Error(); e != nil {
			return nil, e
		}
	}
	row := 0
	for _, r := range d.Files {
		src, e := s.s.Get(ctx, r)
		if e != nil {
			return nil, e
		}
		e = func() error {
			defer src.Close()
			in := bufio.NewReader(src)
			// Strip a UTF-8 BOM before CSV parsing, including when the first header is quoted.
			if prefix, _ := in.Peek(3); bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
				_, _ = in.Discard(3)
			}
			var captured bytes.Buffer
			rd := csv.NewReader(io.TeeReader(in, &captured))
			rd.Comma = delim
			rd.FieldsPerRecord = len(d.Columns)
			header, e := rd.Read()
			if e != nil || !reflect.DeepEqual(header, d.Columns) {
				return errors.New("dataset header mismatch")
			}
			last := rd.InputOffset()
			captured.Next(int(last))
			for {
				rec, e := rd.Read()
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
				for _, s := range rec {
					if !utf8.ValidString(s) {
						return errors.New("dataset is not UTF-8")
					}
				}
				// Preserve quoted CRLF, quoted empty values and escaped quotes byte for byte.
				// csv.Reader canonicalizes line endings in parsed fields, so use its validated
				// byte offsets, not a reserialization. It also skips blank physical lines.
				next := rd.InputOffset()
				record := captured.Next(int(next - last))
				last = next
				for len(record) > 0 {
					if record[0] == '\n' {
						record = record[1:]
						continue
					}
					if bytes.HasPrefix(record, []byte("\r\n")) {
						record = record[2:]
						continue
					}
					break
				}
				if len(record) == 0 {
					return errors.New("empty record span")
				}
				w := writers[row%n]
				if _, e = w.Write(record); e != nil {
					return e
				}
				if record[len(record)-1] != '\n' {
					if e = w.WriteByte('\n'); e != nil {
						return e
					}
				}
				row++
			}
		}()
		if e != nil {
			return nil, e
		}
	}
	if row < n {
		return nil, errors.New("insufficient rows for selected workers")
	}
	for i, w := range writers {
		if e := w.Flush(); e != nil {
			return nil, e
		}
		if e := files[i].Sync(); e != nil {
			return nil, e
		}
	}
	return paths, nil
}
