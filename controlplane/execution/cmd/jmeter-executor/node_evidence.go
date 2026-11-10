package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"pressurecooker/execution/protocol"
)

var nodeLogNames = []string{"jmeter.log", "console.log"}

// Cursors belong to the prepared generation. Bounded prefix fingerprints also
// reject rotation/replacement, rather than attributing another file to the task.
type logCursor struct {
	Valid         bool
	Offset, Lines int64
	Device, Inode uint64
	PrefixSHA256  string
}

type savedNodeEvidence struct {
	Owner    assignment            `json:"owner"`
	Evidence protocol.NodeEvidence `json:"evidence"`
}

func engineLogCursors(dir string) map[string]logCursor {
	out := make(map[string]logCursor, len(nodeLogNames))
	for _, name := range nodeLogNames {
		cursor := logCursor{}
		f, e := openRegular(filepath.Join(dir, name))
		if e != nil {
			out[name] = cursor
			continue
		}
		st, e := f.Stat()
		if e != nil || st.Size() > 1<<20 {
			f.Close()
			out[name] = cursor
			continue
		}
		data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		f.Close()
		stat, ok := st.Sys().(*syscall.Stat_t)
		if e == nil && ok && int64(len(data)) == st.Size() {
			hash := sha256.Sum256(data)
			cursor = logCursor{Valid: true, Offset: st.Size(), Lines: int64(bytes.Count(data, []byte{'\n'})), Device: uint64(stat.Dev), Inode: uint64(stat.Ino), PrefixSHA256: hex.EncodeToString(hash[:])}
		}
		out[name] = cursor
	}
	return out
}

func missingNodeEvidence(node, task, attempt string, generation int64) protocol.NodeEvidence {
	out := protocol.NodeEvidence{NodeID: node, TaskID: task, AttemptID: attempt, Generation: generation, Status: "MISSING", Logs: []protocol.NodeLog{}}
	for _, name := range nodeLogNames {
		out.Logs = append(out.Logs, protocol.NodeLog{Name: name, Status: "MISSING"})
	}
	return out
}

func collectNodeLog(path, name string, cursor logCursor) protocol.NodeLog {
	out := protocol.NodeLog{Name: name, Status: "MISSING"}
	if !cursor.Valid || cursor.Offset < 0 || cursor.Offset > 1<<20 {
		return out
	}
	f, e := openRegular(path)
	if e != nil {
		return out
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || st.Size() < cursor.Offset {
		return out
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Dev) != cursor.Device || uint64(stat.Ino) != cursor.Inode {
		return out
	}
	prefix := make([]byte, cursor.Offset)
	if _, e = io.ReadFull(f, prefix); e != nil {
		return out
	}
	hash := sha256.Sum256(prefix)
	if hex.EncodeToString(hash[:]) != cursor.PrefixSHA256 {
		return out
	}
	data, e := io.ReadAll(io.LimitReader(f, protocol.MaxNodeLogBytes))
	if e != nil {
		return out
	}
	out.Status = "COLLECTED"
	out.Size = st.Size() - cursor.Offset
	out.Content = string(data)
	out.Truncated = out.Size > int64(len(data))
	if len(data) > 0 {
		out.LineFrom = cursor.Lines + 1
	}
	return boundedNodeLog(out, protocol.MaxNodeLogBytes)
}

// Keep a prefix so retained line numbers continue to identify exact source
// lines. The original scoped byte size survives every later aggregate bound.
func boundedNodeLog(log protocol.NodeLog, limit int) protocol.NodeLog {
	if !utf8.ValidString(log.Content) {
		log.Truncated = true
	}
	// A one-byte substitution also keeps the serialized content within the
	// original scoped byte size when a native sampler emits binary output.
	text := strings.ToValidUTF8(log.Content, "?")
	if len(text) > limit {
		end := limit
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
		log.Truncated = true
	}
	if len(text) < len(log.Content) {
		log.Truncated = true
	}
	log.Content = text
	if text == "" {
		log.LineFrom = 0
		log.LineTo = 0
	} else {
		if log.LineFrom < 1 {
			log.LineFrom = 1
		}
		log.LineTo = log.LineFrom + int64(strings.Count(text, "\n"))
		if strings.HasSuffix(text, "\n") {
			log.LineTo--
		}
	}
	return log
}

func evidenceOwnerKey(a assignment) string {
	hash := sha256.Sum256([]byte(a.LeaseToken))
	return hex.EncodeToString(hash[:])
}
func (w *worker) evidenceFile(a assignment) string {
	name := evidenceOwnerKey(a)
	if a.Generation > 0 {
		name += "-" + strconv.FormatInt(a.Generation, 10)
	}
	return filepath.Join(w.s.c.state, "node-evidence", a.TaskID, a.AttemptID, name+".json")
}
func (w *worker) savedEvidence(a assignment) *protocol.NodeEvidence {
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || !protocol.ValidID(a.RunnerID) || a.LeaseToken == "" {
		return nil
	}
	var saved savedNodeEvidence
	if load(w.evidenceFile(a), &saved) != nil || !sameOwner(&saved.Owner, &a) || a.Generation != 0 && a.Generation != saved.Owner.Generation {
		return nil
	}
	e := saved.Evidence
	if e.NodeID != w.s.c.id || e.TaskID != a.TaskID || e.AttemptID != a.AttemptID || e.Generation != saved.Owner.Generation {
		return nil
	}
	return &e
}
func (w *worker) captureEvidence(a assignment, prepared string, cursors map[string]logCursor) {
	if w.savedEvidence(a) != nil {
		return
	}
	if !protocol.ValidID(a.TaskID) || !protocol.ValidID(a.AttemptID) || !protocol.ValidID(a.RunnerID) || a.LeaseToken == "" {
		return
	}
	evidence := missingNodeEvidence(w.s.c.id, a.TaskID, a.AttemptID, a.Generation)
	if prepared != "" && a.Generation > 0 {
		evidence.PreparedAt = prepared
		evidence.FinishedAt = timestamp()
		evidence.Logs = nil
		for _, name := range nodeLogNames {
			log := collectNodeLog(filepath.Join(w.s.c.work, "engine", name), name, cursors[name])
			evidence.Logs = append(evidence.Logs, log)
			if log.Status == "COLLECTED" {
				evidence.Status = "COLLECTED"
			}
		}
	}
	evidence.RequestSamples, evidence.RequestSamplesCaptured, evidence.RequestSamplesTruncated = w.collectRequestSamples(a)
	w.captureThreadObservations(a, &evidence)
	if evidence.RequestSamplesCaptured || evidence.ThreadObservationsStatus == "PENDING" {
		evidence.Status = "COLLECTED"
	}
	// The state directory is supervisor-private; failed collection or persistence
	// never prevents killing/replacing a process. The receipt then reports MISSING.
	w.saveEvidence(a, evidence)
	// A lost prepare response leaves the runner's expected generation unknown.
	// Keep an owner-bound index while retaining every exact generation receipt.
	if a.Generation > 0 {
		index := a
		index.Generation = 0
		_ = save(w.evidenceFile(index), savedNodeEvidence{Owner: a, Evidence: evidence})
	}
}

func validNodeEvidence(e protocol.NodeEvidence, node string, j *journal, generation int64) bool {
	if !protocol.ValidThreadObservations(e) {
		return false
	}
	if e.NodeID != node || e.TaskID != j.Task.TaskID || e.AttemptID != j.Task.AttemptID || e.Generation < 0 || generation > 0 && e.Generation != generation || e.Status != "COLLECTED" && e.Status != "MISSING" || e.Status == "COLLECTED" && e.Generation == 0 || len(e.Logs) > 2 {
		return false
	}
	for _, stamp := range []string{e.PreparedAt, e.FinishedAt} {
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				return false
			}
		}
	}
	if !validateRequestSamples(e) {
		return false
	}
	seen := map[string]bool{}
	for _, log := range e.Logs {
		if log.Name != "jmeter.log" && log.Name != "console.log" || seen[log.Name] || log.Status != "COLLECTED" && log.Status != "MISSING" || len(log.Content) > protocol.MaxNodeLogBytes || !utf8.ValidString(log.Content) || log.Size < int64(len(log.Content)) || log.LineFrom < 0 || log.LineTo < log.LineFrom {
			return false
		}
		if log.Status == "MISSING" && log.Content != "" || e.Status == "MISSING" && log.Status != "MISSING" {
			return false
		}
		seen[log.Name] = true
	}
	return true
}

// Constrain the encoded evidence too: HTML/control escaping can expand a 32KiB
// string sixfold. Selected-worker order survives all truncation and retries.
func boundedNodeEvidence(entries []protocol.NodeEvidence) []protocol.NodeEvidence {
	out := make([]protocol.NodeEvidence, len(entries))
	for i, e := range entries {
		out[i] = e
		out[i].Logs = append([]protocol.NodeLog(nil), e.Logs...)
		var clipped bool
		out[i].RequestSamples, clipped = boundRequestSamples(e.RequestSamples)
		out[i].RequestSamplesTruncated = e.RequestSamplesTruncated || clipped
		for k, log := range out[i].Logs {
			out[i].Logs[k] = boundedNodeLog(log, protocol.MaxNodeLogBytes)
		}
	}
	for {
		b, e := json.Marshal(out)
		if e != nil || len(b) <= protocol.MaxNodeEvidenceBytes {
			return out
		}
		maxI, maxK, maxLen := -1, -1, 0
		for i, e := range out {
			for k, l := range e.Logs {
				if len(l.Content) > maxLen {
					maxI, maxK, maxLen = i, k, len(l.Content)
				}
			}
		}
		// Prefer preserving bounded payloads while reducing oversized log text.
		if maxLen < 256 {
			sampleNode, sampleBytes := -1, 0
			for i, e := range out {
				if len(e.RequestSamples) > 0 {
					raw, _ := json.Marshal(e.RequestSamples)
					if len(raw) > sampleBytes {
						sampleNode, sampleBytes = i, len(raw)
					}
				}
			}
			if sampleNode >= 0 {
				n := len(out[sampleNode].RequestSamples)
				out[sampleNode].RequestSamples = out[sampleNode].RequestSamples[:n/2]
				out[sampleNode].RequestSamplesTruncated = true
				continue
			}
		}
		if maxI < 0 {
			return out
		}
		out[maxI].Logs[maxK] = boundedNodeLog(out[maxI].Logs[maxK], maxLen/2)
	}
}

// Merge by worker, then order using the immutable selected-worker manifest.
func mergeNodeEvidence(j *journal, node string, e protocol.NodeEvidence) {
	byNode := make(map[string]protocol.NodeEvidence, len(j.NodeEvidence)+1)
	for _, old := range j.NodeEvidence {
		byNode[old.NodeID] = old
	}
	byNode[node] = e
	ordered := []protocol.NodeEvidence{}
	for _, w := range j.Task.Workers {
		if evidence, ok := byNode[w.ID]; ok {
			ordered = append(ordered, evidence)
		}
	}
	j.NodeEvidence = boundedNodeEvidence(ordered)
}

func removeEvidence(w *worker, a assignment) {
	if e := w.savedEvidence(a); e != nil {
		a.Generation = e.Generation
		_ = os.Remove(w.threadArchiveFile(a))
		_ = os.Remove(w.threadArchiveFile(a) + ".json")
		_ = os.Remove(w.evidenceFile(a))
		a.Generation = 0
		_ = os.Remove(w.evidenceFile(a))
	}
}

func (w *worker) saveEvidence(a assignment, e protocol.NodeEvidence) {
	_ = save(w.evidenceFile(a), savedNodeEvidence{Owner: a, Evidence: e})
	if a.Generation > 0 {
		index := a
		index.Generation = 0
		_ = save(w.evidenceFile(index), savedNodeEvidence{Owner: a, Evidence: e})
	}
}
