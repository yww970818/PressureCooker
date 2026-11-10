package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type s3Peer struct {
	server       *httptest.Server
	mu           sync.Mutex
	objects      map[string][]byte
	puts         map[string]int
	reject       bool
	badSignature bool
}

func newS3Peer(t *testing.T) *s3Peer {
	s := &s3Peer{objects: map[string][]byte{}, puts: map[string]int{}}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		attrs := map[string]string{}
		authorization := strings.TrimPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
		for _, part := range strings.Split(authorization, ",") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 {
				attrs[kv[0]] = kv[1]
			}
		}
		credential := strings.SplitN(attrs["Credential"], "/", 2)
		if len(credential) != 2 {
			w.WriteHeader(403)
			return
		}
		scope := credential[1]
		signed := attrs["SignedHeaders"]
		var headers strings.Builder
		for _, key := range strings.Split(signed, ";") {
			v := r.Header.Get(key)
			if key == "host" {
				v = r.Host
			}
			headers.WriteString(key + ":" + strings.TrimSpace(v) + "\n")
		}
		canonical := strings.Join([]string{r.Method, r.URL.EscapedPath(), "", headers.String(), signed, digest(body)}, "\n")
		toSign := strings.Join([]string{"AWS4-HMAC-SHA256", r.Header.Get("X-Amz-Date"), scope, digest([]byte(canonical))}, "\n")
		key := []byte("AWS4s3-secret")
		for _, part := range strings.Split(scope, "/") {
			key = hmacSHA(key, part)
		}
		if !constant(attrs["Signature"], hex.EncodeToString(hmacSHA(key, toSign))) {
			s.badSignature = true
			w.WriteHeader(403)
			return
		}
		if s.reject {
			w.WriteHeader(503)
			return
		}
		saved, exists := s.objects[r.URL.Path]
		switch r.Method {
		case "HEAD":
			if !exists {
				w.WriteHeader(404)
			}
		case "GET":
			if !exists {
				w.WriteHeader(404)
			} else {
				w.Write(saved)
			}
		case "PUT":
			if exists && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(412)
				return
			}
			s.objects[r.URL.Path] = body
			s.puts[r.URL.Path]++
			w.WriteHeader(200)
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}
func TestS3StreamingConditionalImmutable(t *testing.T) {
	s := newS3Peer(t)
	cfg := Config{ArchiveEndpoint: s.server.URL, ArchiveBucket: "jmeter-reports", ArchiveRegion: "us-east-1", ArchiveAccessKey: "s3-access", ArchiveSecretKey: "s3-secret"}
	client := &S3Archive{cfg, &http.Client{Timeout: 3 * time.Second}}
	path := filepath.Join(t.TempDir(), "raw")
	content := []byte(strings.Repeat("raw\r\n\x00", 1024*1024))
	if e := os.WriteFile(path, content, 0644); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(content)
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	key := "2026-11-05/run-x/raw/results.jtl"
	if e = client.putStream(key, f, int64(len(content)), hex.EncodeToString(hash[:]), "text/csv"); e != nil {
		t.Fatal(e)
	}
	f.Seek(0, io.SeekStart)
	if e = client.putStream(key, f, int64(len(content)), hex.EncodeToString(hash[:]), "text/csv"); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	if s.badSignature || s.puts["/jmeter-reports/"+key] != 1 {
		t.Fatal("bad signature or duplicate PUT")
	}
	s.objects["/jmeter-reports/"+key] = []byte("different")
	s.mu.Unlock()
	f.Seek(0, io.SeekStart)
	if e = client.putStream(key, f, int64(len(content)), hex.EncodeToString(hash[:]), "text/csv"); e == nil || !strings.Contains(e.Error(), "not overwritten") {
		t.Fatal("changed immutable object accepted", e)
	}
}
func TestInvalidS3Endpoint(t *testing.T) {
	for _, endpoint := range []string{"file:///etc/passwd", "http://user:pass@localhost", "http://localhost/?query=1"} {
		a := &S3Archive{Config{ArchiveEndpoint: endpoint}, &http.Client{Timeout: time.Second}}
		if _, _, e := a.inspect("HEAD", "key"); e == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}
