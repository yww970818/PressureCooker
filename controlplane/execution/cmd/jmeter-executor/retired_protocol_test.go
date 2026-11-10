package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerRejectsUnownedBearerResetBeforeEngineAccess(t *testing.T) {
	s := &service{c: config{token: "synthetic", id: "load-node"}}
	s.worker = &worker{s: s}
	r := httptest.NewRequest("POST", "/reset", strings.NewReader(`{"operation_id":"retired-reset","expected_generation":0}`))
	r.Header.Set("Authorization", "Bearer synthetic")
	w := httptest.NewRecorder()
	s.handler(w, r)
	if w.Code != 401 {
		t.Fatal("unowned reset was accepted", w.Code)
	}
}
