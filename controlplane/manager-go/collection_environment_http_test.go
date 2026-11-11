package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMySQLCollectionEnvironmentHTTPDeleteRevision(t *testing.T) {
	p, _ := mysqlFixture(t)
	collection, err := p.workspaceCreate("api_collections", resourceCollection([]any{}))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := p.collectionEnvironmentResourcesRequest(str(collection["id"]), "", http.MethodPost, Obj{"revision": 1, "name": "HTTP delete", "base_host": "https://fixture.invalid", "variables": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/api-collections/" + str(collection["id"]) + "/collection-environments/" + str(obj(receipt["environment"])["id"])
	request := func(project, credential string, revision int, expected int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodDelete, path, bytes.NewReader(encoded(Obj{"revision": revision})))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Project-ID", project)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("DELETE status=%d want=%d: %s", w.Code, expected, w.Body.String())
		}
	}
	request(p.projectID(), "wrong-credential", 2, http.StatusUnauthorized)
	request(p.projectID(), p.Config.AdminToken, 1, http.StatusConflict)
	request(p.projectID(), p.Config.AdminToken, 2, http.StatusOK)
	listed, err := p.collectionEnvironmentResourcesRequest(str(collection["id"]), "", http.MethodGet, nil)
	if err != nil || len(arr(listed["items"])) != 0 || number(listed["revision"]) != 3 {
		t.Fatalf("DELETE did not persist revision: %v %v", listed, err)
	}
}
