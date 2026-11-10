package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type proxySessionDriver struct{}
type proxySessionConn struct{}
type proxySessionRows struct{ done bool }
type proxyProjectRows struct {
	done     bool
	archived bool
}

func (proxySessionDriver) Open(string) (driver.Conn, error)  { return proxySessionConn{}, nil }
func (proxySessionConn) Prepare(string) (driver.Stmt, error) { return nil, io.EOF }
func (proxySessionConn) Close() error                        { return nil }
func (proxySessionConn) Begin() (driver.Tx, error)           { return nil, io.EOF }
func (proxySessionConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(q, "FROM projects") && len(args) == 1 {
		if args[0].Value == "project-private-context" || args[0].Value == defaultProjectID {
			return &proxyProjectRows{}, nil
		}
		if args[0].Value == "project-archived" {
			return &proxyProjectRows{archived: true}, nil
		}
		return emptySessionsRows{}, nil
	}
	if len(args) != 1 || args[0].Value != sessionHash("proxy-session-key", "valid-cookie") {
		return emptySessionsRows{}, nil
	}
	return &proxySessionRows{}, nil
}
func (*proxyProjectRows) Columns() []string { return []string{"data"} }
func (*proxyProjectRows) Close() error      { return nil }
func (r *proxyProjectRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = string(encoded(Obj{"archived": r.archived}))
	return nil
}
func (*proxySessionRows) Columns() []string { return []string{"csrf", "expires"} }
func (*proxySessionRows) Close() error      { return nil }
func (r *proxySessionRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = "csrf-secret"
	values[1] = epoch() + 3600
	return nil
}
func init() { sql.Register("proxy-test-session", proxySessionDriver{}) }
func proxyPlatform(t *testing.T, target *httptest.Server) *Platform {
	t.Helper()
	db, e := sql.Open("proxy-test-session", "")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	serverURL, _ := url.Parse(target.URL)
	port := serverURL.Port()
	cfg := Config{AdminToken: "platform-admin", SessionKey: "proxy-session-key"}
	cfg.proxyTestResolver = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host != "service.internal" {
			t.Errorf("unexpected DNS target %q", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("192.168.50.10")}}, nil
	}
	cfg.proxyTestDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("192.168.50.10", port) {
			t.Errorf("dial not pinned to approved IP: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	return &Platform{Config: cfg, DB: &Database{DB: db}}
}
func makeProxyRequest(target *httptest.Server, method string, body io.Reader) *http.Request {
	u, _ := url.Parse(target.URL)
	destination := "http://service.internal:" + u.Port() + "/request?q=1"
	r := httptest.NewRequest(method, "/api/api-proxy?scalar_url="+url.QueryEscape(destination), body)
	r.Header.Set("Cookie", "jmeter_session=valid-cookie; other=platform-cookie-secret")
	r.Header.Set("X-JMeter-CSRF-Token", "csrf-secret")
	r.Header.Set("X-JMeter-Project-ID", "project-private-context")
	return r
}
func TestScalarProxyUsesSessionCSRFAndPreservesBusinessProtocol(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "PATCH" || string(body) != `{"x":"中文"}` || r.Header.Get("Authorization") != "Bearer target-business-secret" || r.Header.Get("X-CSRF-Token") != "business-csrf-secret" || r.Header.Get("Cookie") != "business_session=only-target" || r.Header.Get("User-Agent") != "Target Agent" || r.Header.Get("Dnt") != "1" || r.Header.Get("Referer") != "https://business.example/ref" {
			t.Error("business request changed", r.Method, r.Header, string(body))
		}
		if strings.Contains(r.Host, "127.0.0.1") || r.URL.Path != "/request" || r.URL.RawQuery != "q=1" {
			t.Error("original Host or target URL changed", r.Host, r.URL)
		}
		for _, key := range []string{"X-JMeter-CSRF-Token", "X-JMeter-Project-ID", "Origin", "X-Forwarded-For", "X-Hop", "X-Scalar-Cookie", "X-Scalar-User-Agent"} {
			if r.Header.Get(key) != "" {
				t.Error("platform/hop header leaked", key)
			}
		}
		if strings.Contains(r.Header.Get("Cookie"), "platform-cookie-secret") {
			t.Error("platform cookie leaked")
		}
		w.Header().Add("Set-Cookie", "target_session=one; HttpOnly")
		w.Header().Add("Set-Cookie", "target_session_two=two; Path=/")
		w.Header().Set("Connection", "X-Private")
		w.Header().Set("X-Private", "must-strip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()
	p := proxyPlatform(t, target)
	for _, test := range []struct {
		cookie, csrf, authorization string
		status                      int
	}{{"", "", "Bearer platform-admin", 401}, {"jmeter_session=valid-cookie", "", "Bearer target-business-secret", 403}, {"jmeter_session=invalid-cookie", "csrf-secret", "Bearer target-business-secret", 401}} {
		r := makeProxyRequest(target, "GET", nil)
		r.Header.Set("Cookie", test.cookie)
		r.Header.Set("X-JMeter-CSRF-Token", test.csrf)
		r.Header.Set("Authorization", test.authorization)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatal("platform authentication confused with target auth", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized target request sent")
	}
	r := makeProxyRequest(target, "PATCH", strings.NewReader(`{"x":"中文"}`))
	r.Header.Set("Authorization", "Bearer target-business-secret")
	r.Header.Set("X-CSRF-Token", "business-csrf-secret")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://platform.example")
	r.Header.Set("Referer", "https://platform.example/page")
	r.Header.Set("X-Forwarded-For", "must-strip")
	r.Header.Set("Connection", "X-Hop")
	r.Header.Set("X-Hop", "must-strip")
	r.Header.Set("X-Scalar-Cookie", "business_session=only-target")
	r.Header.Set("X-Scalar-User-Agent", "Target Agent")
	r.Header.Set("X-Scalar-Dnt", "1")
	r.Header.Set("X-Scalar-Referer", "https://business.example/ref")
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 201 || w.Body.String() != `{"ok":true}` || w.Header().Get("Set-Cookie") != "" || len(w.Header().Values("X-Scalar-Set-Cookie")) != 2 || w.Header().Get("X-Private") != "" {
		t.Fatal("unsafe/incomplete proxy response", w.Code, w.Header(), w.Body.String())
	}
}
func TestScalarProxyRejectsUnsafeTargetsAndMixedDNS(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "0.0.0.0", "0.12.1.2", "169.254.169.254", "fe80::1", "ff02::1", "224.0.0.1", "100.100.100.200", "168.63.129.16", "fd00:ec2::254", "64:ff9b::7f00:1"} {
		if safeProxyIP(net.ParseIP(value)) {
			t.Errorf("unsafe target permitted %s", value)
		}
	}
	for _, value := range []string{"10.0.0.10", "172.25.0.5", "192.168.1.2", "8.8.8.8", "fd12:3456::1"} {
		if !safeProxyIP(net.ParseIP(value)) {
			t.Errorf("private/public unicast rejected %s", value)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:pass@service.internal/", "http://service.internal:0/", "http://service.internal/#fragment"} {
		if _, e := proxyTarget(raw); e == nil {
			t.Error("unsafe URL permitted", raw)
		}
	}
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer target.Close()
	p := proxyPlatform(t, target)
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.168.50.10")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, makeProxyRequest(target, "GET", nil))
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("mixed unsafe DNS reached target", w.Code, calls.Load())
	}
}
func TestScalarProxyRejectsRedirectsOversizeAndTimeout(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.Header.Get("X-Case") {
		case "redirect":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", 302)
		case "oversize":
			w.Header().Set("Content-Length", "5242880")
			w.WriteHeader(200)
		case "oversize-stream":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			io.WriteString(w, strings.Repeat("a", proxyResponseLimit+1))
		case "timeout":
			io.Copy(io.Discard, r.Body)
			time.Sleep(200 * time.Millisecond)
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer target.Close()
	p := proxyPlatform(t, target)
	for _, name := range []string{"redirect", "oversize", "oversize-stream"} {
		r := makeProxyRequest(target, "GET", nil)
		r.Header.Set("X-Case", name)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != 502 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "169.254") {
			t.Fatal(name, w.Code, w.Header(), w.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatal("redirect followed", calls.Load())
	}
	r := makeProxyRequest(target, "POST", strings.NewReader(strings.Repeat("a", proxyRequestLimit+1)))
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 413 || calls.Load() != 3 {
		t.Fatal("oversize request sent", w.Code, calls.Load())
	}
	r = makeProxyRequest(target, "GET", nil)
	r.Header.Set("X-Case", "timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	r = r.WithContext(ctx)
	w = httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 504 {
		t.Fatal("unbounded proxy request", w.Code, w.Body.String())
	}
}

func TestScalarProxyHEADMetadataAndNotModifiedStatus(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", "5242880")
			w.WriteHeader(200)
			return
		}
		w.Header().Set("ETag", `"unchanged"`)
		w.WriteHeader(304)
	}))
	defer target.Close()
	p := proxyPlatform(t, target)
	for _, method := range []string{"HEAD", "GET"} {
		r := makeProxyRequest(target, method, nil)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if method == "HEAD" {
			if w.Code != 200 || w.Header().Get("Content-Length") != "5242880" || w.Body.Len() != 0 {
				t.Fatal("HEAD response metadata changed", w.Code, w.Header(), w.Body.String())
			}
		} else if w.Code != 304 || w.Header().Get("ETag") != `"unchanged"` || w.Body.Len() != 0 {
			t.Fatal("304 treated as followed redirect", w.Code, w.Header(), w.Body.String())
		}
	}
}

func TestScalarProxyProjectContextFailsClosedBeforeSending(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	p := proxyPlatform(t, target)
	for _, tc := range []struct {
		name   string
		values []string
		want   int
	}{
		{"missing", nil, 400}, {"empty", []string{""}, 400}, {"duplicate", []string{"project-private-context", "project-private-context"}, 400}, {"invalid", []string{"../project"}, 400}, {"unknown", []string{"project-does-not-exist"}, 404}, {"archived", []string{"project-archived"}, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := makeProxyRequest(target, "POST", strings.NewReader("change-state"))
			r.Header.Del("X-JMeter-Project-ID")
			for _, value := range tc.values {
				r.Header.Add("X-JMeter-Project-ID", value)
			}
			w := httptest.NewRecorder()
			Handler{p}.ServeHTTP(w, r)
			if w.Code != tc.want || calls.Load() != 0 {
				t.Fatalf("invalid project reached target: code=%d calls=%d", w.Code, calls.Load())
			}
		})
	}
	for _, id := range []string{defaultProjectID, "project-private-context"} {
		r := makeProxyRequest(target, "GET", nil)
		r.Header.Set("X-JMeter-Project-ID", id)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatal("valid explicit project rejected", id, w.Code, w.Body.String())
		}
	}
}

func TestScalarProxyNeverForwardsPlatformBearerToTarget(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	p := proxyPlatform(t, target)
	r := makeProxyRequest(target, "POST", strings.NewReader("change-state"))
	r.Header.Set("Authorization", "Bearer "+p.Config.AdminToken)
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	if w.Code != 400 || calls.Load() != 0 {
		t.Fatalf("platform credential reached HTTP target: code=%d target_calls=%d", w.Code, calls.Load())
	}
}

func TestScalarProxyRejectsPlatformBearerInAnyAuthorizationValue(t *testing.T) {
	for _, values := range [][]string{
		{"Basic business", "Bearer platform-admin"},
		{"Basic business, Bearer platform-admin"},
		{"Bearer business, Bearer platform-admin"},
		{"Bearer platform-admin trailing"},
		{"Bearer platform-admin;scope=read"},
		{`Bearer "platform-admin"`},
		{"Bearer platform-admin=padding"},
		{"Bearer other platform-admin"},
	} {
		var calls atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
		p := proxyPlatform(t, target)
		r := makeProxyRequest(target, "GET", nil)
		r.Header.Del("Authorization")
		for _, value := range values {
			r.Header.Add("Authorization", value)
		}
		err := p.apiProxy(httptest.NewRecorder(), r)
		target.Close()
		failure, ok := err.(*APIError)
		if !ok || failure.Code != "platform_authorization_forbidden" || calls.Load() != 0 {
			t.Errorf("platform credential reached target: error=%v calls=%d", err, calls.Load())
		}
	}
}

func TestScalarProxyKeepsUnrelatedBusinessAuthorization(t *testing.T) {
	for _, authorization := range []string{"Basic YnVzaW5lc3M6b25seQ==", "Bearer platform-admin-unrelated", `Digest username="fixture", realm="business", nonce="value", response="012345"`} {
		received := make(chan string, 1)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received <- r.Header.Get("Authorization")
			w.WriteHeader(204)
		}))
		p := proxyPlatform(t, target)
		r := makeProxyRequest(target, "GET", nil)
		r.Header.Set("Authorization", authorization)
		err := p.apiProxy(httptest.NewRecorder(), r)
		target.Close()
		if err != nil {
			t.Fatal("business authorization was rejected", err)
		}
		select {
		case got := <-received:
			if got != authorization {
				t.Fatal("business authorization changed")
			}
		default:
			t.Fatal("target was not contacted")
		}
	}
}
