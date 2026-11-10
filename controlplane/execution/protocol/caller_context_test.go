package protocol

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func callerContextRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", "http://manager/api/environments?revision=7%2B1", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Project-ID", "project-one")
	return r
}

func TestCallerContextIndependentHMACVectorAndNormalization(t *testing.T) {
	r := callerContextRequest(t)
	at := time.UnixMilli(1700000000123)
	if err := SignCallerContext(r, "2001:0db8::1", "test-admin-secret", at); err != nil {
		t.Fatal(err)
	}
	if r.Header.Get(CallerIPHeader) != "2001:db8::1" || r.Header.Get(CallerTimeHeader) != "1700000000123" || r.Header.Get(CallerSignatureHeader) != "acb05c4f6b19976402413e513229d7a54861d5e0ca04e66db6bae4afd40c087b" {
		t.Fatal("caller context HMAC wire contract changed")
	}
	if ip, err := VerifyCallerContext(r, "test-admin-secret", at); err != nil || ip != "2001:db8::1" {
		t.Fatal(ip, err)
	}
	for _, tc := range []struct{ input, want string }{{"::ffff:192.0.2.4", "192.0.2.4"}, {"", ""}} {
		if err := SignCallerContext(r, tc.input, "token", at); err != nil {
			t.Fatal(err)
		}
		if got, err := VerifyCallerContext(r, "token", at); err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
}

func TestCallerContextRejectsTamperingPartialDuplicateAndExpiredHeaders(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		token  string
		now    time.Time
	}{
		{name: "method", mutate: func(r *http.Request) { r.Method = "GET" }},
		{name: "path", mutate: func(r *http.Request) { r.URL.Path = "/api/projects" }},
		{name: "query", mutate: func(r *http.Request) { r.URL.RawQuery = "revision=7+1" }},
		{name: "project", mutate: func(r *http.Request) { r.Header.Set("X-Project-ID", "project-other") }},
		{name: "IP", mutate: func(r *http.Request) { r.Header.Set(CallerIPHeader, "192.0.2.5") }},
		{name: "bad IP", mutate: func(r *http.Request) { r.Header.Set(CallerIPHeader, "peer.example") }},
		{name: "zone", mutate: func(r *http.Request) { r.Header.Set(CallerIPHeader, "fe80::1%en0") }},
		{name: "timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "1700000000124") }},
		{name: "noncanonical timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "01700000000123") }},
		{name: "nonnumeric timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "not-time") }},
		{name: "overflow timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "9223372036854775808") }},
		{name: "zero timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "0") }},
		{name: "negative timestamp", mutate: func(r *http.Request) { r.Header.Set(CallerTimeHeader, "-1") }},
		{name: "signature", mutate: func(r *http.Request) { r.Header.Set(CallerSignatureHeader, strings.Repeat("0", 64)) }},
		{name: "short signature", mutate: func(r *http.Request) { r.Header.Set(CallerSignatureHeader, "abc") }},
		{name: "uppercase signature", mutate: func(r *http.Request) {
			r.Header.Set(CallerSignatureHeader, strings.ToUpper(r.Header.Get(CallerSignatureHeader)))
		}},
		{name: "missing IP", mutate: func(r *http.Request) { r.Header.Del(CallerIPHeader) }},
		{name: "missing time", mutate: func(r *http.Request) { r.Header.Del(CallerTimeHeader) }},
		{name: "missing signature", mutate: func(r *http.Request) { r.Header.Del(CallerSignatureHeader) }},
		{name: "duplicate IP", mutate: func(r *http.Request) { r.Header.Add(CallerIPHeader, "192.0.2.4") }},
		{name: "duplicate time", mutate: func(r *http.Request) { r.Header.Add(CallerTimeHeader, r.Header.Get(CallerTimeHeader)) }},
		{name: "duplicate signature", mutate: func(r *http.Request) { r.Header.Add(CallerSignatureHeader, r.Header.Get(CallerSignatureHeader)) }},
		{name: "case duplicate", mutate: func(r *http.Request) { r.Header[strings.ToLower(CallerIPHeader)] = []string{"192.0.2.4"} }},
		{name: "wrong token", token: "wrong-token"},
		{name: "expired", now: at.Add(2*time.Minute + time.Millisecond)},
		{name: "future", now: at.Add(-2*time.Minute - time.Millisecond)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := callerContextRequest(t)
			if err := SignCallerContext(r, "192.0.2.4", "token", at); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(r)
			}
			token := tc.token
			if token == "" {
				token = "token"
			}
			now := tc.now
			if now.IsZero() {
				now = at
			}
			if _, err := VerifyCallerContext(r, token, now); err == nil {
				t.Fatal("invalid caller attribution accepted")
			}
		})
	}
	for _, delta := range []time.Duration{-2 * time.Minute, 2 * time.Minute} {
		r := callerContextRequest(t)
		_ = SignCallerContext(r, "", "token", at)
		if _, err := VerifyCallerContext(r, "token", at.Add(delta)); err != nil {
			t.Fatal("exact window boundary rejected", err)
		}
	}
}

func TestCallerContextRejectsInvalidSigningInputsAndMissingContext(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	for _, ip := range []string{"192.0.2.4:80", " peer", "peer.example", "fe80::1%en0", "192.0.2.4\n"} {
		if err := SignCallerContext(callerContextRequest(t), ip, "token", at); err == nil {
			t.Fatal("invalid source signed", ip)
		}
	}
	if err := SignCallerContext(nil, "", "token", at); err == nil {
		t.Fatal("nil request signed")
	}
	if err := SignCallerContext(callerContextRequest(t), "", "", at); err == nil {
		t.Fatal("empty credential signed")
	}
	if _, err := VerifyCallerContext(callerContextRequest(t), "token", at); err == nil {
		t.Fatal("missing caller context accepted")
	}
	if _, err := VerifyCallerContext(nil, "token", at); err == nil {
		t.Fatal("nil request accepted")
	}
	r := callerContextRequest(t)
	r.Header.Add(CallerIPHeader, "forged")
	r.Header.Add(CallerIPHeader, "forged-again")
	r.Header[strings.ToLower(CallerSignatureHeader)] = []string{"forged"}
	if err := SignCallerContext(r, "192.0.2.4", "token", at); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyCallerContext(r, "token", at); err != nil {
		t.Fatal("signing retained forged or duplicate metadata", err)
	}
	if _, err := VerifyCallerContext(r, "", at); err == nil {
		t.Fatal("empty verification credential accepted")
	}
}
