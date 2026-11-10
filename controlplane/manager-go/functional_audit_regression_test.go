package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugExplicitAuthorizationStillRequiresAPIKeys(t *testing.T) {
	for _, location := range []string{"header", "query"} {
		t.Run(location, func(t *testing.T) {
			document := Obj{"security": []any{Obj{"bearer": []any{}, "key": []any{}}}, "components": Obj{"securitySchemes": Obj{
				"bearer": Obj{"type": "http", "scheme": "bearer", "x-scalar-secret-token": "{{unused}}"},
				"key":    Obj{"type": "apiKey", "in": location, "name": "X-API-Key", "x-scalar-secret-token": "{{key}}"},
			}}}
			headers := http.Header{"Authorization": {"Bearer explicit-credential"}}
			query := []string{}
			if err := applyDebugSecurity(document, Obj{}, headers, &query, map[string]string{"key": "synthetic+key"}); err != nil {
				t.Fatal(err)
			}
			if headers.Get("Authorization") != "Bearer explicit-credential" {
				t.Fatal("explicit Authorization was replaced")
			}
			if location == "header" && headers.Get("X-API-Key") != "synthetic+key" {
				t.Fatal("AND authentication lost its API key header")
			}
			if location == "query" && (len(query) != 1 || query[0] != "X-API-Key=synthetic%2Bkey") {
				t.Fatal("AND authentication lost its API key query parameter", query)
			}
			delete(obj(obj(obj(document["components"])["securitySchemes"])["key"]), "x-scalar-secret-token")
			if err := applyDebugSecurity(document, Obj{}, headers, &query, nil); err == nil {
				t.Fatal("explicit Authorization bypassed a missing required API key")
			}
		})
	}
}

func TestPlanUploadRejectsNonJMeterXMLBeforeSaving(t *testing.T) {
	for _, content := range []string{"<html/>", "<TestPlan/>", "<wrapper><jmeterTestPlan/></wrapper>", `<jmeterTestPlan xmlns="urn:other"/>`} {
		t.Run(content, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil {
				t.Fatal(err)
			}
			p := &Platform{Config: Config{PlansDir: root}}
			if _, err = p.preparePlan(Obj{"name": "invalid upload", "content": content}); err == nil {
				t.Fatal("non-JMeter XML accepted as an executable plan")
			}
			files, err := os.ReadDir(filepath.Join(root, "snapshots"))
			if err != nil || len(files) != 0 {
				t.Fatal("invalid plan left a snapshot", err)
			}
		})
	}
}
