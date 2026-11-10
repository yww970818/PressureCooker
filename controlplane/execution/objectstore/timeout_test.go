package objectstore

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"pressurecooker/execution/protocol"
)

func timeoutArchive(endpoint string, idle time.Duration) *Client {
	c, e := New(Config{Endpoint: endpoint, Bucket: "bucket", AccessKey: "a", SecretKey: "s", Timeout: idle})
	if e != nil {
		panic(e)
	}
	return c
}
func verifyProgress(a *Client, payload string) error {
	r, e := a.Get(context.Background(), protocol.ObjectRef{Bucket: "bucket", Key: "key", SHA256: digest([]byte(payload)), Size: int64(len(payload))})
	if e != nil {
		return e
	}
	defer r.Close()
	_, e = io.Copy(io.Discard, r)
	return e
}
func TestObjectStoreProgressingGETExceedsIdleTimeout(t *testing.T) {
	payload := strings.Repeat("a", 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8")
		for _, c := range payload {
			io.WriteString(w, string(c))
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	}))
	defer server.Close()
	a := timeoutArchive(server.URL, 100*time.Millisecond)
	started := time.Now()
	if e := verifyProgress(a, payload); e != nil {
		t.Fatalf("progressing GET failed after %s: %v", time.Since(started), e)
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Fatal("test did not exceed idle timeout")
	}
}

type progressingReader struct {
	remaining int
	chunk     []byte
	delay     time.Duration
}

func (r *progressingReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	n := copy(p, r.chunk)
	if n > r.remaining {
		n = r.remaining
	}
	r.remaining -= n
	return n, nil
}
func TestObjectStoreProgressingPUTExceedsIdleTimeout(t *testing.T) {
	const size = 8 * 65536
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, e := io.Copy(io.Discard, r.Body)
		if e != nil || n != size {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	a := timeoutArchive(server.URL, 100*time.Millisecond)
	started := time.Now()
	source := &progressingReader{size, bytes.Repeat([]byte("a"), 65536), 20 * time.Millisecond}
	response, e := a.request(context.Background(), "PUT", "key", source, size, digest(bytes.Repeat([]byte("a"), size)), "text/plain")
	if e != nil || response.StatusCode != 200 {
		t.Fatalf("progressing PUT failed after %s: %v %v", time.Since(started), response, e)
	}
	response.Body.Close()
	if time.Since(started) < 250*time.Millisecond {
		t.Fatal("test did not exceed idle timeout")
	}
}
func TestObjectStoreStalledGETTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8")
		io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		time.Sleep(400 * time.Millisecond)
		io.WriteString(w, "aaaaaaa")
	}))
	defer server.Close()
	a := timeoutArchive(server.URL, 100*time.Millisecond)
	started := time.Now()
	if e := verifyProgress(a, "aaaaaaaa"); e == nil {
		t.Fatal("stalled GET did not time out")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("idle GET timeout too slow: %s", elapsed)
	}
}
func TestObjectStoreStalledPUTTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	a := timeoutArchive(server.URL, 100*time.Millisecond)
	const size = 32 * 1024 * 1024
	started := time.Now()
	response, e := a.request(context.Background(), "PUT", "key", io.LimitReader(repeatByteReader{}, size), size, strings.Repeat("0", 64), "text/plain")
	if e == nil {
		t.Fatalf("stalled PUT succeeded: %v", response)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("idle PUT timeout too slow: %s", elapsed)
	}
}

type repeatByteReader struct{}

func (repeatByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}
