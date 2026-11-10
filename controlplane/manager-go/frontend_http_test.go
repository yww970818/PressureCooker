package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func frontendFileFixture(t *testing.T) (Handler, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":                       "<html>entry version one</html>",
		"favicon.svg":                      "<svg/>",
		"assets/index-A1b2C3d4.js":         "console.log('version one');",
		"assets/theme-aB_cD-12.css":        "body{color:red}",
		"assets/fonts/font-aB_cD-12.woff2": "font fixture",
		"assets/runtime.js":                "public runtime settings",
		"assets/preview-A1b2C3d4.html":     "<html>preview</html>",
		"outside-A1b2C3d4.js":              "outside the build assets namespace",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return Handler{&Platform{Config: Config{FrontendDir: root, AdminToken: "fixture-admin"}}}, root
}

func frontendRequest(h Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestFrontendCachePolicyAndHead(t *testing.T) {
	h, _ := frontendFileFixture(t)
	for _, tc := range []struct{ path, policy string }{
		{"/", "no-cache"}, {"/index.html", "no-cache"}, {"/tasks/history", "no-cache"},
		{"/favicon.svg", "no-cache"}, {"/assets/runtime.js", "no-cache"},
		{"/assets/preview-A1b2C3d4.html", "no-cache"}, {"/outside-A1b2C3d4.js", "no-cache"},
		{"/assets/index-A1b2C3d4.js", "public, max-age=31536000, immutable"},
		{"/assets/theme-aB_cD-12.css?v=ignored", "public, max-age=31536000, immutable"},
		{"/assets/fonts/font-aB_cD-12.woff2", "public, max-age=31536000, immutable"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			get := frontendRequest(h, "GET", tc.path, nil)
			head := frontendRequest(h, "HEAD", tc.path, nil)
			for _, w := range []*httptest.ResponseRecorder{get, head} {
				if w.Code != 200 || w.Header().Get("Cache-Control") != tc.policy || w.Header().Get("ETag") == "" || strings.HasPrefix(w.Header().Get("ETag"), "W/") {
					t.Fatalf("status=%d headers=%v", w.Code, w.Header())
				}
			}
			if head.Body.Len() != 0 || head.Header().Get("ETag") != get.Header().Get("ETag") || head.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) {
				t.Fatal("HEAD must preserve GET metadata without its body")
			}
		})
	}
}

func TestFrontendETagRevalidation(t *testing.T) {
	h, _ := frontendFileFixture(t)
	for _, path := range []string{"/", "/tasks/history", "/assets/index-A1b2C3d4.js"} {
		first := frontendRequest(h, "GET", path, nil)
		tag := first.Header().Get("ETag")
		if tag == "" {
			t.Fatalf("missing validator for %s", path)
		}
		for _, method := range []string{"GET", "HEAD"} {
			for _, condition := range []string{tag, "W/" + tag, `"older", ` + tag, "*"} {
				w := frontendRequest(h, method, path, map[string]string{"If-None-Match": condition})
				if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("ETag") != tag || w.Header().Get("Cache-Control") != first.Header().Get("Cache-Control") {
					t.Fatalf("%s %s with %s: status=%d headers=%v bytes=%d", method, path, condition, w.Code, w.Header(), w.Body.Len())
				}
			}
		}
	}
}

func TestFrontendEntryChangesInvalidateEvenWithSameSizeAndMtime(t *testing.T) {
	h, root := frontendFileFixture(t)
	path := filepath.Join(root, "index.html")
	stamp := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	old := frontendRequest(h, "GET", "/", nil)
	const content = "<html>entry version two</html>"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	w := frontendRequest(h, "GET", "/", map[string]string{"If-None-Match": old.Header().Get("ETag"), "If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)})
	if old.Header().Get("ETag") == "" || w.Code != 200 || w.Body.String() != content || w.Header().Get("ETag") == old.Header().Get("ETag") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("new entry reused an old validator: status=%d headers=%v", w.Code, w.Header())
	}
}

func TestFrontendErrorsAndAPIsStayUncacheable(t *testing.T) {
	h, root := frontendFileFixture(t)
	if err := os.Symlink(filepath.Join(root, "index.html"), filepath.Join(root, "assets/link-A1b2C3d4.js")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, auth string
		status             int
	}{
		{"GET", "/assets/missing-A1b2C3d4.js", "", 404},
		{"GET", "/assets/link-A1b2C3d4.js", "", 400},
		{"POST", "/assets/index-A1b2C3d4.js", "", 405},
		{"GET", "/api/session", "Bearer wrong", 401},
		{"GET", "/api/session", "Bearer fixture-admin", 200},
		{"GET", "/healthz", "", 200},
	} {
		w := frontendRequest(h, tc.method, tc.path, map[string]string{"Authorization": tc.auth, "If-None-Match": "*"})
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
			t.Fatalf("%s %s: status=%d headers=%v", tc.method, tc.path, w.Code, w.Header())
		}
	}
}

func TestFrontendRangeAndPreconditionErrorsAreNotCached(t *testing.T) {
	h, _ := frontendFileFixture(t)
	const path = "/assets/index-A1b2C3d4.js"
	w := frontendRequest(h, "GET", path, map[string]string{"Range": "bytes=0-2"})
	if w.Code != 206 || w.Body.String() != "con" || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("valid range: status=%d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	for _, debug := range []string{"", "httpservecontentkeepheaders=1"} {
		t.Run(debug, func(t *testing.T) {
			t.Setenv("GODEBUG", debug)
			for _, tc := range []struct {
				headers map[string]string
				status  int
			}{
				{map[string]string{"Range": "bytes=999999-"}, 416},
				{map[string]string{"If-Match": `"does-not-match"`}, 412},
			} {
				w := frontendRequest(h, "GET", path, tc.headers)
				if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
					t.Fatalf("precondition error: status=%d headers=%v", w.Code, w.Header())
				}
			}
		})
	}
}

func TestPrivateFileDownloadsDoNotInheritFrontendCaching(t *testing.T) {
	_, root := frontendFileFixture(t)
	r := httptest.NewRequest("GET", "/api/plans/fixture/content", nil)
	r.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	w.Header().Set("Cache-Control", "no-store")
	if err := sendFile(w, r, filepath.Join(root, "assets/index-A1b2C3d4.js"), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Body.Len() == 0 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
		t.Fatal("private download inherited frontend validators or public caching")
	}
}
