package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var fingerprintedFrontendAsset = regexp.MustCompile(`^assets/([^/]+/)*[^/]+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

// Only public frontend files receive validators or long-lived cache headers.
// Authenticated artifacts continue through sendFile with the handler's no-store.
func sendFrontendFile(w http.ResponseWriter, r *http.Request, path, relative string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return api(404, "not_found", "File not found")
	}
	// Hash contents instead of relying on size/mtime: releases may preserve both
	// while changing index.html. Stream the file to keep large bundles bounded.
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	policy := "no-cache"
	extension := strings.ToLower(filepath.Ext(relative))
	if extension != ".html" && extension != ".htm" && fingerprintedFrontendAsset.MatchString(filepath.ToSlash(relative)) {
		policy = "public, max-age=31536000, immutable"
	}
	w.Header().Set("Cache-Control", policy)
	w.Header().Set("ETag", `"`+hex.EncodeToString(hash.Sum(nil))+`"`)
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	// ETag is authoritative. Omitting modification time prevents same-second
	// releases from being incorrectly validated by If-Modified-Since alone.
	http.ServeContent(frontendResponse{w}, r, filepath.Base(path), time.Time{}, f)
	return nil
}

type frontendResponse struct{ http.ResponseWriter }

func (w frontendResponse) WriteHeader(status int) {
	if status >= 400 {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("ETag")
		w.Header().Del("Last-Modified")
	}
	w.ResponseWriter.WriteHeader(status)
}

// Keep the underlying server's optimized file copy path when available.
func (w frontendResponse) ReadFrom(r io.Reader) (int64, error) {
	if reader, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return reader.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}
