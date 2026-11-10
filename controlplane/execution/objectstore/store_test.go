package objectstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pressurecooker/execution/protocol"
)

func TestCorruptDownloadDoesNotReplaceOrFollowLink(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("bad")) }))
	defer svr.Close()
	s, _ := New(Config{Endpoint: svr.URL, Bucket: "bucket", AccessKey: "key", SecretKey: "secret"})
	r := protocol.ObjectRef{Bucket: "bucket", Key: "dir/file", SHA256: digest([]byte("yes")), Size: 3}
	d := t.TempDir()
	d, _ = filepath.EvalSymlinks(d)
	dst := filepath.Join(d, "result")
	os.WriteFile(dst, []byte("old"), 0600)
	if e := s.Download(context.Background(), r, dst); e == nil {
		t.Fatal("corruption accepted")
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "old" {
		t.Fatal("lost good file")
	}
	link := filepath.Join(d, "link")
	os.Symlink(dst, link)
	if e := s.Download(context.Background(), r, link); e == nil {
		t.Fatal("followed link")
	}
	r.Key = "../outside"
	if _, e := s.Get(context.Background(), r); e == nil {
		t.Fatal("traversal accepted")
	}
}
func TestIsolatedMinIOImmutableRoundTrip(t *testing.T) {
	if os.Getenv("JMETER_EXECUTION_IT") != "1" {
		t.Skip("enable isolated MinIO integration")
	}
	read := func(name string) string {
		b, e := os.ReadFile("../../../runtime/local-secrets/" + name)
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(string(b))
	}
	s, e := New(Config{Endpoint: "http://127.0.0.1:29000", Bucket: "jmeter-reports", AccessKey: read("minio-access-key"), SecretKey: read("minio-secret-key")})
	if e != nil {
		t.Fatal(e)
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	key := "it/" + hex.EncodeToString(nonce[:]) + "/immutable.csv"
	ctx := context.Background()
	defer func() {
		if e := s.Delete(ctx, key); e != nil {
			t.Error(e)
		}
	}()
	b := []byte("id,value\n1,中文\n")
	put := func(b []byte) (protocol.ObjectRef, error) {
		return s.Put(ctx, key, bytes.NewReader(b), int64(len(b)), digest(b), "text/csv")
	}
	r, e := put(b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = put(b); e != nil {
		t.Fatal(e)
	}
	if _, e = put([]byte("changed")); e == nil {
		t.Fatal("conflicting content accepted")
	}
	rd, e := s.Get(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	got, e := io.ReadAll(rd)
	rd.Close()
	if e != nil || !bytes.Equal(got, b) {
		t.Fatal("invalid content")
	}
	d := t.TempDir()
	d, _ = filepath.EvalSymlinks(d)
	dst := filepath.Join(d, "file.csv")
	if e = s.Download(ctx, r, dst); e != nil {
		t.Fatal(e)
	}
	st, _ := os.Stat(dst)
	if st.Mode().Perm() != 0600 {
		t.Fatal("download permissions")
	}
	r.Bucket = "foreign"
	if _, e = s.Head(ctx, r); e == nil {
		t.Fatal("foreign bucket permitted")
	}
}
