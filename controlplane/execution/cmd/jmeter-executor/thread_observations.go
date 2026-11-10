package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"pressurecooker/execution/protocol"
)

type threadCaptureState struct {
	Size     int64
	SHA256   string
	Complete bool
}

const threadObserverClass = "io.pressurecooker.jmeter.ThreadObservationListener"

func (c config) threadObserverEngineArgs(generation int64) []string {
	return append([]string{"-Dpressurecooker.capture.generation=" + strconv.FormatInt(generation, 10), "-Dpressurecooker.capture.node_id=" + c.id}, c.baseArgs()...)
}

// The observer is a TestStateListener, never a sampler or sample listener.
// It is deserialized by the official runtime on both controller and workers.
func injectThreadObservations(raw []byte, t protocol.Task, ownerKey string) ([]byte, error) {
	if !protocol.ValidID(t.TaskID) || !protocol.ValidID(t.AttemptID) || len(ownerKey) != 64 {
		return nil, errors.New("invalid thread observer identity")
	}
	if _, err := hex.DecodeString(ownerKey); err != nil {
		return nil, errors.New("invalid thread observer owner")
	}
	if bytes.Contains(raw, []byte(threadObserverClass)) {
		return nil, errors.New("duplicate thread observer")
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth, planDepth := 0, 0
	awaitTree := false
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := tok.(type) {
		case xml.StartElement:
			depth++
			if awaitTree {
				if v.Name.Local != "hashTree" || depth != planDepth {
					return nil, errors.New("missing TestPlan scope")
				}
				var element bytes.Buffer
				fmt.Fprintf(&element, `<%s guiclass="TestBeanGUI" testclass="%s" testname="Independent thread observations" enabled="true">`, threadObserverClass, threadObserverClass)
				for _, prop := range []struct{ k, v string }{{"task_id", t.TaskID}, {"attempt_id", t.AttemptID}, {"owner_key", ownerKey}} {
					fmt.Fprintf(&element, `<stringProp name="%s">`, prop.k)
					xml.EscapeText(&element, []byte(prop.v))
					element.WriteString(`</stringProp>`)
				}
				fmt.Fprintf(&element, `</%s><hashTree/>`, threadObserverClass)
				after := int(dec.InputOffset())
				if bytes.HasSuffix(bytes.TrimSpace(raw[before:after]), []byte("/>")) {
					slash := bytes.LastIndex(raw[before:after], []byte("/>"))
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

func (w *worker) threadCaptureDir(a assignment) string {
	return filepath.Join(w.s.taskDir(a.TaskID), "thread-observations", a.AttemptID)
}
func (w *worker) threadArchiveFile(a assignment) string {
	return strings.TrimSuffix(w.evidenceFile(a), ".json") + ".thread-observations.csv"
}

func (w *worker) threadBinding(a assignment) (requestCaptureBinding, error) {
	var b requestCaptureBinding
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || a.Generation < 1 {
		return b, errors.New("invalid thread observer owner")
	}
	err := load(filepath.Join(w.threadCaptureDir(a), "binding.json"), &b)
	if err != nil {
		return b, err
	}
	if b.TaskID != a.TaskID || b.AttemptID != a.AttemptID || b.NodeID != w.s.c.id || b.Generation != a.Generation || b.OwnerKey != evidenceOwnerKey(a) || b.File != evidenceOwnerKey(a)+"-"+strconv.FormatInt(a.Generation, 10)+".csv" {
		return b, errors.New("thread observer binding mismatch")
	}
	return b, nil
}

func (w *worker) initializeThreadObservations(a assignment) error {
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || !protocol.ValidID(w.s.c.id) || a.Generation < 1 {
		return errors.New("invalid thread observer owner")
	}
	dir := w.threadCaptureDir(a)
	if prior, err := w.threadBinding(a); err == nil {
		f, err := openThreadObservationFile(filepath.Join(dir, prior.File), prior)
		if err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		return w.writeThreadBindingProperties(a, prior)
	}
	if err := directory(dir); err != nil {
		return err
	}
	name := evidenceOwnerKey(a) + "-" + strconv.FormatInt(a.Generation, 10) + ".csv"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("pressurecooker_thread_observations_v1,%s,%s,%s,%d,%d\ntimestamp_ms,active_threads\n", a.TaskID, a.AttemptID, w.s.c.id, a.Generation, protocol.ThreadObservationIntervalMS)
	_, err = f.WriteString(header)
	if err == nil {
		err = f.Sync()
	}
	st, statErr := f.Stat()
	f.Close()
	if err != nil {
		return err
	}
	if statErr != nil {
		return statErr
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("thread observer file identity unavailable")
	}
	b := requestCaptureBinding{TaskID: a.TaskID, AttemptID: a.AttemptID, NodeID: w.s.c.id, Generation: a.Generation, OwnerKey: evidenceOwnerKey(a), File: name, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
	if err = save(filepath.Join(dir, "binding.json"), b); err != nil {
		return err
	}
	return w.writeThreadBindingProperties(a, b)
}

func (w *worker) writeThreadBindingProperties(a assignment, b requestCaptureBinding) error {
	dir := w.threadCaptureDir(a)
	// Only fixed ASCII identities and the prepared basename enter this read-only
	// local binding. No paths or lease tokens are shipped in the distributed JMX.
	properties := fmt.Sprintf("task_id=%s\nattempt_id=%s\nnode_id=%s\ngeneration=%d\nowner_key=%s\nfile=%s\ndevice=%d\ninode=%d\n", b.TaskID, b.AttemptID, b.NodeID, b.Generation, b.OwnerKey, b.File, b.Device, b.Inode)
	if err := writeFile(filepath.Join(dir, "binding.properties"), []byte(properties)); err != nil {
		return err
	}
	if w.s.c.isolated {
		if err := engineDirectories(dir, w.s.taskDir(a.TaskID)); err != nil {
			return err
		}
		if err := grant(filepath.Join(dir, "binding.properties"), false, 0640); err != nil {
			return err
		}
		return grant(filepath.Join(dir, b.File), false, 0660)
	}
	return nil
}

func openThreadObservationFile(path string, b requestCaptureBinding) (*os.File, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil {
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || uint64(stat.Dev) != b.Device || uint64(stat.Ino) != b.Inode || stat.Nlink != 1 {
			err = errors.New("thread observer file identity changed")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (w *worker) captureThreadObservations(a assignment, e *protocol.NodeEvidence) {
	e.ThreadObservationsStatus = "MISSING"
	b, err := w.threadBinding(a)
	if err != nil {
		return
	}
	e.ThreadObservationsStatus = "INVALID"
	f, err := openThreadObservationFile(filepath.Join(w.threadCaptureDir(a), b.File), b)
	if err != nil {
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, protocol.MaxThreadObservationBytes+1))
	if err != nil || int64(len(raw)) > protocol.MaxThreadObservationBytes {
		return
	}
	count := 0
	complete, err := protocol.ReadThreadObservations(bytes.NewReader(raw), *e, func(protocol.ThreadObservation) error { count++; return nil })
	if err != nil {
		return
	}
	if count == 0 {
		e.ThreadObservationsStatus = "MISSING"
		return
	}
	if err = writeFile(w.threadArchiveFile(a), raw); err != nil {
		return
	}
	sum := sha256.Sum256(raw)
	if err = save(w.threadArchiveFile(a)+".json", threadCaptureState{Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:]), Complete: complete}); err != nil {
		return
	}
	e.ThreadObservationsStatus = "PENDING"
}

func (w *worker) publishThreadObservations(ctx context.Context, a assignment) *protocol.NodeEvidence {
	e := w.savedEvidence(a)
	if e == nil || e.ThreadObservationsStatus != "PENDING" {
		return e
	}
	a.Generation = e.Generation
	invalid := func() *protocol.NodeEvidence {
		e.ThreadObservationsStatus = "INVALID"
		e.ThreadObservations = nil
		w.saveEvidence(a, *e)
		return e
	}
	var state threadCaptureState
	if load(w.threadArchiveFile(a)+".json", &state) != nil || state.Size <= 0 || state.Size > protocol.MaxThreadObservationBytes {
		return invalid()
	}
	key := protocol.ThreadObservationsKey(*e, state.SHA256)
	if key == "" {
		return invalid()
	}
	// Verify the frozen private source before choosing a SHA-addressed key.
	// A damaged snapshot cannot poison that immutable key or retry forever.
	f, err := openRegular(w.threadArchiveFile(a))
	if err != nil {
		return invalid()
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(f, protocol.MaxThreadObservationBytes+1))
	f.Close()
	if err != nil || size != state.Size || hex.EncodeToString(hash.Sum(nil)) != state.SHA256 {
		return invalid()
	}
	if w.s.s == nil {
		return e
	}
	ref, err := w.s.putFile(ctx, key, w.threadArchiveFile(a), "text/csv; charset=utf-8")
	if err != nil || ref.Size != state.Size || ref.SHA256 != state.SHA256 {
		return e
	}
	ref.Name = "thread-observations.csv"
	e.ThreadObservations = &ref
	e.ThreadObservationsStatus = "ARCHIVED"
	if !state.Complete {
		e.ThreadObservationsStatus = "INCOMPLETE"
	}
	w.saveEvidence(a, *e)
	return e
}
