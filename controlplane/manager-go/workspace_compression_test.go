package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Build the compressed envelope independently of sealWorkspace so a reader-only
// rollback binary can prove compatibility while retaining its previous writer.
func TestWorkspaceReadsCompressedEnvelope(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "compression-fixture"}}
	want := Obj{"name": "fixture", "graph": Obj{"nested": []any{Obj{"body": strings.Repeat("中文<&>", 20000)}, true, nil, 42}}}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(encoded(want)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	aead, err := p.workspaceCipher()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	payload := "gcm2:" + base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, compressed.Bytes(), append(workspaceAAD("workflows", "fixture", 1), []byte("\x00gzip")...)))
	got, err := p.openWorkspace("workflows", "fixture", 1, payload)
	if err != nil || !bytes.Equal(encoded(got), encoded(want)) {
		t.Fatal("compressed envelope unreadable", err)
	}
}

func TestMySQLWorkflowCompressedBackupRestoreRetainsBothFormats(t *testing.T) {
	p, _ := mysqlFixture(t)
	graph := basicWorkflowGraph()
	created, err := p.workspaceCreate("workflows", Obj{"name": "mixed history", "graph": graph})
	if err != nil {
		t.Fatal(err)
	}
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = strings.Repeat("synthetic compressed body 中文 ", 10000)
	updated, err := p.workspaceUpdate("workflows", str(created["id"]), Obj{"name": "compressed history", "revision": 1, "graph": graph})
	if err != nil {
		t.Fatal(err)
	}
	var old, current string
	if err = p.DB.QueryRow("SELECT payload FROM workflow_versions WHERE object_id=? AND revision=1", created["id"]).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", created["id"]).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(old, "gcm1:") || !strings.HasPrefix(current, "gcm2:") {
		t.Fatal("fixture must cover both formats")
	}
	archive := filepath.Join(t.TempDir(), "backup")
	if _, err = p.backup(archive); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restore")
	if _, err = restoreArchive(archive, destination); err != nil {
		t.Fatal(err)
	}
	restored := emptyWorkspacePlatform(t, p, destination)
	state, err := loadJSONFile(filepath.Join(destination, "state", "manager.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.importState(state); err != nil {
		t.Fatal(err)
	}
	read, err := restored.workspaceGet("workflows", str(created["id"]))
	if err != nil || !bytes.Equal(encoded(read), encoded(updated)) {
		t.Fatal("compressed backup changed", err)
	}
	read, err = restored.workspaceVersions("workflows", str(created["id"]), 1)
	if err != nil || !bytes.Equal(encoded(read), encoded(created)) {
		t.Fatal("old backup changed", err)
	}
	var restoredCipher string
	if err = restored.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", created["id"]).Scan(&restoredCipher); err != nil || restoredCipher != current {
		t.Fatal("restore rewrote ciphertext", err)
	}
	if _, err = restored.workflowEditorRead(str(created["id"])); err != nil {
		t.Fatal("editor cannot open restored compressed flow", err)
	}
}

func TestWorkspaceCompressedWorkflowPreservesLegacyAndAuthentication(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "compression-fixture"}}
	graph := basicWorkflowGraph()
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = strings.Repeat("中文 <>& payload ", 100000)
	data := Obj{"name": "压缩测试", "graph": graph}
	payload, err := p.sealWorkspace("workflows", "workflow-compressed", 3, data)
	if err != nil || !strings.HasPrefix(payload, "gcm2:") || len(payload) > len(encoded(data))/4 {
		t.Fatal("large workflow not compacted", err)
	}
	read, err := p.openWorkspace("workflows", "workflow-compressed", 3, payload)
	if err != nil || !bytes.Equal(encoded(read), encoded(data)) {
		t.Fatal("compressed graph changed", err)
	}
	second, err := p.sealWorkspace("workflows", "workflow-compressed", 3, data)
	if err != nil || second == payload || strings.Contains(payload, "payload") {
		t.Fatal("nonce or confidentiality failure", err)
	}
	for _, test := range []struct {
		table, id string
		revision  int64
		payload   string
	}{
		{"api_collections", "workflow-compressed", 3, payload},
		{"workflows", "workflow-other", 3, payload},
		{"workflows", "workflow-compressed", 4, payload},
		{"workflows", "workflow-compressed", 3, "gcm1:" + payload[5:]},
		{"workflows", "workflow-compressed", 3, payload[:len(payload)-4] + "AAAA"},
	} {
		if _, err = p.openWorkspace(test.table, test.id, test.revision, test.payload); err == nil {
			t.Fatal("unauthenticated compressed payload accepted")
		}
	}
	// Construct the previous envelope independently, as an old saved revision.
	aead, err := p.workspaceCipher()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	legacy := "gcm1:" + base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, encoded(data), workspaceAAD("workflows", "workflow-compressed", 2)))
	read, err = p.openWorkspace("workflows", "workflow-compressed", 2, legacy)
	if err != nil || !bytes.Equal(encoded(read), encoded(data)) {
		t.Fatal("legacy workflow no longer readable", err)
	}
	for _, table := range []string{"api_collections", "environments", "task_dispatches"} {
		value, err := p.sealWorkspace(table, "fixture", 1, data)
		if err != nil || !strings.HasPrefix(value, "gcm1:") {
			t.Fatal("unrelated encryption format changed", table, err)
		}
	}
	// A later lower capacity must not prevent opening an older large revision.
	t.Setenv("JMETER_WORKFLOW_BODY_MIB", "1")
	if _, err = p.openWorkspace("workflows", "workflow-compressed", 3, payload); err != nil {
		t.Fatal("lower capacity made old data unreadable", err)
	}
	p.Config.SessionKey = "wrong-key"
	if _, err = p.openWorkspace("workflows", "workflow-compressed", 3, payload); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestWorkspaceCompressedWorkflowRejectsInvalidStreams(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "compression-fixture"}}
	aead, err := p.workspaceCipher()
	if err != nil {
		t.Fatal(err)
	}
	pack := func(raw []byte) []byte {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err := z.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	valid := pack([]byte(`{"name":"test"}`))
	badChecksum := append([]byte(nil), valid...)
	badChecksum[len(badChecksum)-8] ^= 1
	for _, raw := range [][]byte{[]byte("not gzip"), valid[:len(valid)-4], badChecksum, append(append([]byte(nil), valid...), valid...), pack([]byte("not JSON")), pack(bytes.Repeat([]byte(" "), (65<<20)+1))} {
		nonce := make([]byte, aead.NonceSize())
		payload := "gcm2:" + base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, append(workspaceAAD("workflows", "fixture", 1), []byte("\x00gzip")...)))
		if _, err = p.openWorkspace("workflows", "fixture", 1, payload); err == nil {
			t.Fatal("invalid/oversized compressed stream accepted")
		}
	}
}

func TestMySQLWorkflowCSVProjectionBatchesRemainAtomic(t *testing.T) {
	p, _ := mysqlFixture(t)
	graph := basicWorkflowGraph()
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("csv-%03d", i)
		graph["nodes"] = append(arr(graph["nodes"]), graphNode(id, "CSVDataSet", Obj{"filename": "", "variableNames": fmt.Sprintf("value_%d", i)}))
		graph["edges"] = append(arr(graph["edges"]), graphEdge("group", id, i+1))
	}
	created, err := p.workspaceCreate("workflows", Obj{"name": "batched CSV", "graph": graph})
	if err != nil {
		t.Fatal(err)
	}
	verify := func(revision, count int) {
		t.Helper()
		rows, err := p.DB.Query("SELECT node_id,position,revision,payload FROM workflow_csv_nodes WHERE workflow_id=? ORDER BY position", created["id"])
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			var id, payload string
			var position, rev int
			if err = rows.Scan(&id, &position, &rev, &payload); err != nil {
				t.Fatal(err)
			}
			if rev != revision || position != 3+n || id != fmt.Sprintf("csv-%03d", n) {
				t.Fatal("batch order/revision changed")
			}
			if _, err = p.openWorkspace("csv-node/"+str(created["id"]), id, int64(rev), payload); err != nil {
				t.Fatal(err)
			}
			n++
		}
		if err = rows.Err(); err != nil || n != count {
			t.Fatal("batch rows missing", n, err)
		}
	}
	verify(1, 205)
	// Fail after all batches and editor cache have been written: rollback must
	// preserve the previous projection and source/history ciphertext exactly.
	var before string
	if err = p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", created["id"]).Scan(&before); err != nil {
		t.Fatal(err)
	}
	rollback := fmt.Errorf("synthetic rollback")
	if err = p.transaction(func(tx *Tx) error {
		next := clone(created)
		next["name"] = "rolled back projection"
		if err := p.writeWorkspaceProjection(tx, "workflows", next); err != nil {
			return err
		}
		return rollback
	}); err != rollback {
		t.Fatal(err)
	}
	verify(1, 205)
	obj(obj(arr(graph["nodes"])[len(arr(graph["nodes"]))-1])["properties"])["enabled"] = false
	if _, err = p.workspaceUpdate("workflows", str(created["id"]), Obj{"name": "updated batches", "revision": 1, "graph": graph}); err != nil {
		t.Fatal(err)
	}
	verify(2, 204)
	var historical string
	if err = p.DB.QueryRow("SELECT payload FROM workflow_versions WHERE object_id=? AND revision=1", created["id"]).Scan(&historical); err != nil || historical != before {
		t.Fatal("history ciphertext changed", err)
	}
}
