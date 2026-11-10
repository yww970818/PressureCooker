package objectstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeletePrefixBoundsAndPagination(t *testing.T) {
	requests := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Query().Get("prefix") != "2026-11-09/run-one/" || r.URL.Query().Get("list-type") != "2" {
			t.Error("missing signed list query", r.URL.RawQuery)
		}
		if r.URL.Query().Get("continuation-token") == "" {
			w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next+/=</NextContinuationToken><Contents><Key>2026-11-09/run-one/raw/results.jtl</Key></Contents></ListBucketResult>`))
		} else {
			w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>2026-11-09/run-one/raw/console.log</Key></Contents></ListBucketResult>`))
		}
	}))
	defer peer.Close()
	c, e := New(Config{Endpoint: peer.URL, Bucket: "bucket", AccessKey: "a", SecretKey: "s"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.DeletePrefix(context.Background(), "2026-11-09/run-one/"); e != nil {
		t.Fatal(e)
	}
	if len(requests) != 4 {
		t.Fatal(requests)
	}
	for _, bad := range []string{"", "/", "plans/../", "2026-11-09/run-one"} {
		if e = c.DeletePrefix(context.Background(), bad); e == nil {
			t.Fatal("unsafe prefix accepted", bad)
		}
	}
}
func TestDeletePrefixRejectsEscapingList(t *testing.T) {
	deleted := false
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
		}
		w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>plans/shared.jmx</Key></Contents></ListBucketResult>`))
	}))
	defer peer.Close()
	c, e := New(Config{Endpoint: peer.URL, Bucket: "bucket", AccessKey: "a", SecretKey: "s"})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.DeletePrefix(context.Background(), "2026-11-09/run-one/"); e == nil || !strings.Contains(e.Error(), "prefix") || deleted {
		t.Fatal("unsafe listing", deleted, e)
	}
}
