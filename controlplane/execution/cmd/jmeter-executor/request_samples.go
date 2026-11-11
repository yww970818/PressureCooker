package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
)

//go:embed request_capture.groovy
var requestCaptureScript string

const captureMarker = "pressurecooker.request_samples.v1"

// Postprocessors run after the sample but before assertions/listener delivery.
// A package-local clone is also necessary: parent TransactionControllers remove
// listeners shared by identity with their child package. No private API is used.
const requestCaptureOrderScript = `
try {
 def pack=vars.getObject(org.apache.jmeter.threads.JMeterThread.PACKAGE_OBJECT)
 if(pack==null) return
 def listeners=pack.getSampleListeners()
 if(listeners.isEmpty()) return
 def marker='pressurecooker.request_samples.v1'
 def first=listeners.get(0)
 if(first instanceof org.apache.jmeter.visualizers.JSR223Listener && first.getPropertyAsString('cacheKey')==marker && first.getPropertyAsBoolean('pressurecooker.capture.pack_local')) return
 def source=listeners.find { it instanceof org.apache.jmeter.visualizers.JSR223Listener && it.getPropertyAsString('cacheKey')==marker }
 if(source==null) return
 def local=source.clone()
 local.setRunningVersion(false)
 local.setProperty('pressurecooker.capture.pack_local',true)
 local.setRunningVersion(true)
 listeners.removeAll { it instanceof org.apache.jmeter.visualizers.JSR223Listener && it.getPropertyAsString('cacheKey')==marker }
 listeners.add(0,local)
} catch(Exception ignored) { }
`

type requestCaptureBinding struct {
	TaskID     string `json:"task_id"`
	AttemptID  string `json:"attempt_id"`
	NodeID     string `json:"node_id"`
	Generation int64  `json:"generation"`
	OwnerKey   string `json:"owner_key"`
	File       string `json:"file"`
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
}
type requestCaptureFile struct {
	TaskID     string                   `json:"task_id"`
	AttemptID  string                   `json:"attempt_id"`
	Generation int64                    `json:"generation"`
	OwnerKey   string                   `json:"owner_key"`
	Samples    []protocol.RequestSample `json:"samples"`
	Truncated  bool                     `json:"truncated"`
}

// Insert first in the TestPlan's scope: distributed ResultCollector listeners
// can strip response data in place. Keep every unrelated original XML byte.
func injectRequestCapture(raw []byte, t protocol.Task, ownerKey string) ([]byte, error) {
	if !protocol.ValidID(t.TaskID) || !protocol.ValidID(t.AttemptID) || ownerKey == "" || strings.ContainsAny(ownerKey, " \r\n\t") {
		return nil, errors.New("invalid capture identity")
	}
	if bytes.Contains(raw, []byte(captureMarker)) {
		return nil, errors.New("duplicate request collector")
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth := 0
	planDepth := 0
	awaitTree := false
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
			if awaitTree {
				if v.Name.Local != "hashTree" || depth != planDepth {
					return nil, errors.New("missing TestPlan scope")
				}
				var element bytes.Buffer
				element.WriteString(`<JSR223Listener guiclass="TestBeanGUI" testclass="JSR223Listener" testname="Bounded request evidence" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="cacheKey">` + captureMarker + `</stringProp><stringProp name="parameters">`)
				xml.EscapeText(&element, []byte(t.TaskID+" "+t.AttemptID+" "+ownerKey))
				element.WriteString(`</stringProp><stringProp name="script">`)
				xml.EscapeText(&element, []byte(requestCaptureScript))
				element.WriteString(`</stringProp></JSR223Listener><hashTree/>`)
				element.WriteString(`<JSR223PostProcessor guiclass="TestBeanGUI" testclass="JSR223PostProcessor" testname="Order bounded request evidence" enabled="true"><stringProp name="scriptLanguage">groovy</stringProp><stringProp name="cacheKey">pressurecooker.request_samples.order.v1</stringProp><stringProp name="script">`)
				xml.EscapeText(&element, []byte(requestCaptureOrderScript))
				element.WriteString(`</stringProp></JSR223PostProcessor><hashTree/>`)
				after := int(dec.InputOffset())
				if bytes.HasSuffix(bytes.TrimSpace(raw[before:after]), []byte("/>")) {
					prefix := raw[before:after]
					slash := bytes.LastIndex(prefix, []byte("/>"))
					out := append([]byte{}, raw[:before+slash]...)
					out = append(out, '>')
					out = append(out, element.Bytes()...)
					out = append(out, []byte("</hashTree>")...)
					out = append(out, raw[after:]...)
					return out, nil
				}
				out := append([]byte{}, raw[:after]...)
				out = append(out, element.Bytes()...)
				out = append(out, raw[after:]...)
				return out, nil
			}
			if v.Name.Local == "TestPlan" {
				if planDepth != 0 {
					return nil, errors.New("multiple TestPlan elements")
				}
				planDepth = depth
			}
		case xml.EndElement:
			if v.Name.Local == "TestPlan" && depth == planDepth {
				awaitTree = true
			}
			depth--
		}
	}
	return nil, errors.New("TestPlan scope missing")
}
func (w *worker) requestCaptureDir(a assignment) string {
	return filepath.Join(w.s.taskDir(a.TaskID), "request-samples", a.AttemptID)
}
func (w *worker) initializeRequestCapture(a assignment) error {
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || a.Generation <= 0 {
		return errors.New("invalid capture owner")
	}
	dir := w.requestCaptureDir(a)
	bindingPath := filepath.Join(dir, "binding.json")
	var prior requestCaptureBinding
	if load(bindingPath, &prior) == nil && prior.OwnerKey == evidenceOwnerKey(a) && prior.Generation == a.Generation {
		return nil
	}
	if e := directory(dir); e != nil {
		return e
	}
	name := evidenceOwnerKey(a) + "-" + strconv.FormatInt(a.Generation, 10) + ".json"
	path := filepath.Join(dir, name)
	initial := requestCaptureFile{TaskID: a.TaskID, AttemptID: a.AttemptID, Generation: a.Generation, OwnerKey: evidenceOwnerKey(a), Samples: []protocol.RequestSample{}}
	if e := save(path, initial); e != nil {
		return e
	}
	st, e := os.Stat(path)
	if e != nil {
		return e
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("capture file identity unavailable")
	}
	binding := requestCaptureBinding{TaskID: a.TaskID, AttemptID: a.AttemptID, NodeID: w.s.c.id, Generation: a.Generation, OwnerKey: evidenceOwnerKey(a), File: name, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
	if e = save(bindingPath, binding); e != nil {
		return e
	}
	if w.s.c.isolated {
		if e = engineDirectories(dir, w.s.taskDir(a.TaskID)); e != nil {
			return e
		}
		if e = grant(bindingPath, false, 0640); e != nil {
			return e
		}
		return grant(path, false, 0660)
	}
	return nil
}
func (w *worker) collectRequestSamples(a assignment) ([]protocol.RequestSample, bool, bool) {
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || a.Generation <= 0 {
		return nil, false, false
	}
	dir := w.requestCaptureDir(a)
	var binding requestCaptureBinding
	if load(filepath.Join(dir, "binding.json"), &binding) != nil || binding.TaskID != a.TaskID || binding.AttemptID != a.AttemptID || binding.NodeID != w.s.c.id || binding.Generation != a.Generation || binding.OwnerKey != evidenceOwnerKey(a) || filepath.Base(binding.File) != binding.File {
		return nil, false, false
	}
	f, e := openRegular(filepath.Join(dir, binding.File))
	if e != nil {
		return nil, false, false
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, false, false
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Dev) != binding.Device || uint64(stat.Ino) != binding.Inode || stat.Nlink != 1 {
		return nil, false, false
	}
	// The listener's envelope has a small fixed identity overhead beyond samples.
	if st.Size() > protocol.MaxRequestSamplesBytes+4096 {
		return nil, false, true
	}
	raw, e := io.ReadAll(io.LimitReader(f, protocol.MaxRequestSamplesBytes+4097))
	if e != nil {
		return nil, false, true
	}
	var data requestCaptureFile
	if json.Unmarshal(raw, &data) != nil || data.TaskID != a.TaskID || data.AttemptID != a.AttemptID || data.Generation != a.Generation || data.OwnerKey != evidenceOwnerKey(a) {
		return nil, false, true
	}
	samples, truncated := boundRequestSamples(data.Samples)
	return samples, true, data.Truncated || truncated
}
func sampleText(value string, limit int) (string, bool) {
	text := strings.ToValidUTF8(value, "?")
	truncated := text != value
	if len(text) > limit {
		end := limit
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
		truncated = true
	}
	return text, truncated
}
func boundRequestSamples(samples []protocol.RequestSample) ([]protocol.RequestSample, bool) {
	var out []protocol.RequestSample
	truncated := false
	for _, sample := range samples {
		if len(out) >= protocol.MaxRequestSamples {
			truncated = true
			break
		}
		if sample.Label == "" || len(sample.Label) > 4096 || !utf8.ValidString(sample.Label) || sample.TimestampMS < 0 || sample.ElapsedMS < 0 {
			truncated = true
			continue
		}
		sample.StepKey = "" // Never trust a collector-provided report projection key.
		if sample.SampleKind != "ordinary" && sample.SampleKind != "transaction" {
			sample.SampleKind = "unknown"
		}
		fields := []struct {
			value **string
			limit int
		}{{&sample.Method, 32}, {&sample.URL, 4096}, {&sample.RequestParams, 4096}, {&sample.ResponseCode, 256}, {&sample.ResponseBody, 8192}}
		for _, field := range fields {
			if *field.value != nil {
				text, clipped := sampleText(**field.value, field.limit)
				*field.value = &text
				sample.Truncated = sample.Truncated || clipped
			}
		}
		assertions := append([]protocol.RequestAssertion(nil), sample.Assertions...)
		if len(assertions) > 16 {
			assertions = assertions[:16]
			sample.Truncated = true
		}
		for i := range assertions {
			name, clipped := sampleText(assertions[i].Name, 128)
			message, clippedMessage := sampleText(assertions[i].Message, 512)
			assertions[i].Name = name
			assertions[i].Message = message
			sample.Truncated = sample.Truncated || clipped || clippedMessage
		}
		sample.Assertions = assertions
		if sample.AssertionsCaptured && sample.Assertions == nil {
			sample.Assertions = []protocol.RequestAssertion{}
		}
		candidate := append(out, sample)
		encoded, e := json.Marshal(candidate)
		if e != nil || len(encoded) > protocol.MaxRequestSamplesBytes {
			truncated = true
			break
		}
		out = candidate
	}
	return out, truncated
}
func validateRequestSamples(e protocol.NodeEvidence) bool {
	if len(e.RequestSamples) > 0 && !e.RequestSamplesCaptured || e.RequestSamplesCaptured && e.Status != "COLLECTED" {
		return false
	}
	if len(e.RequestSamples) > protocol.MaxRequestSamples {
		return false
	}
	encoded, err := json.Marshal(e.RequestSamples)
	if err != nil || len(encoded) > protocol.MaxRequestSamplesBytes {
		return false
	}
	for _, s := range e.RequestSamples {
		if s.Label == "" || len(s.Label) > 4096 || !utf8.ValidString(s.Label) || s.TimestampMS < 0 || s.ElapsedMS < 0 || len(s.Assertions) > 16 {
			return false
		}
		for _, f := range []struct {
			v *string
			n int
		}{{s.Method, 32}, {s.URL, 4096}, {s.RequestParams, 4096}, {s.ResponseCode, 256}, {s.ResponseBody, 8192}} {
			if f.v != nil && (len(*f.v) > f.n || !utf8.ValidString(*f.v)) {
				return false
			}
		}
		for _, a := range s.Assertions {
			if len(a.Name) > 128 || len(a.Message) > 512 || !utf8.ValidString(a.Name) || !utf8.ValidString(a.Message) {
				return false
			}
		}
	}
	return true
}
