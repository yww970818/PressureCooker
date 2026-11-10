package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Cookie header can contain credentials regardless of its property spelling
// or whether its runtime alias hides that spelling. Test the real public
// projections of frozen history rather than a regex or a mutable environment.
func TestMySQLCookieValuesRedactedInHistoryAndLog(t *testing.T) {
	p, _ := mysqlFixture(t)
	props := Obj{"model": "visible-model", "threads": "8"}
	variables, overrides := []any{}, []any{}
	secrets := map[string]string{}
	for i, name := range []string{"cookie", "Cookie", "http_cookie", "api_token", "password", "Authorization"} {
		global := fmt.Sprintf("global-session-%d-abcdef", i)
		project := fmt.Sprintf("project-session-%d-abcdef", i)
		collection := fmt.Sprintf("collection-session-%d-abcdef", i)
		envKey, apiKey := environmentProperty(name), apiEnvironmentProperty("request-one", name)
		props[name] = global
		props[envKey] = project
		props[apiKey] = collection
		variables = append(variables, Obj{"name": name, "value": project})
		overrides = append(overrides, Obj{"name": name, "property": apiKey, "value": collection})
		secrets[name] = global
		secrets[envKey] = project
		secrets[apiKey] = collection
	}
	for _, name := range []string{"model", "threads"} {
		props[environmentProperty(name)] = props[name]
		apiKey := apiEnvironmentProperty("request-one", name)
		props[apiKey] = props[name]
		variables = append(variables, Obj{"name": name, "value": props[name]})
		overrides = append(overrides, Obj{"name": name, "property": apiKey, "value": props[name]})
	}
	id := newID("run-cookie-history-")
	frozen := Obj{"id": id, "name": "cookie historical fixture", "project_id": p.projectID(), "status": "STOPPED", "created_at": now(), "properties": props, "environment_id": "environment-frozen-cookie", "environment_snapshot": Obj{"id": "environment-frozen-cookie", "project_id": p.projectID(), "variables": variables, "api_overrides": overrides}}
	stored, e := p.runForStorage(frozen)
	if e != nil {
		t.Fatal(e)
	}
	rawBefore := string(encoded(stored))
	if _, e = p.DB.Exec("INSERT INTO runs(id,status,created_at,data,project_id) VALUES(?,?,?,?,?)", id, "STOPPED", frozen["created_at"], rawBefore, p.projectID()); e != nil {
		t.Fatal(e)
	}
	checkPublic := func(label string, run Obj) {
		t.Helper()
		for key := range secrets {
			if obj(run["properties"])[key] != "***" {
				t.Errorf("%s exposes sensitive property %s", label, key)
			}
		}
		snapshot := obj(run["environment_snapshot"])
		for _, field := range []string{"variables", "api_overrides"} {
			for _, v := range arr(snapshot[field]) {
				pair := obj(v)
				name := str(pair["name"])
				if name == "model" || name == "threads" {
					if pair["value"] != props[name] {
						t.Errorf("%s hides frozen %s %s", label, field, name)
					}
					continue
				}
				if pair["value"] != "***" {
					t.Errorf("%s exposes frozen %s sensitive %s", label, field, name)
				}
			}
		}
		for _, name := range []string{"model", "threads"} {
			for _, key := range []string{name, environmentProperty(name), apiEnvironmentProperty("request-one", name)} {
				if obj(run["properties"])[key] != props[name] {
					t.Errorf("%s hides normal %s", label, key)
				}
			}
		}
		for _, secret := range secrets {
			if strings.Contains(string(encoded(run)), secret) {
				t.Errorf("%s contains raw sensitive value", label)
			}
		}
	}
	public, e := p.getRun(id)
	if e != nil {
		t.Fatal(e)
	}
	checkPublic("detail", public)
	list, e := p.listRuns("", "", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(list["items"])) != 1 {
		t.Fatal("missing historical fixture", list)
	}
	checkPublic("list", obj(arr(list["items"])[0]))
	root := filepath.Join(p.Config.ResultsDir, id, "raw")
	if e = os.MkdirAll(root, 0755); e != nil {
		t.Fatal(e)
	}
	lines := []string{"model=visible-model threads=8"}
	for key, value := range secrets {
		lines = append(lines, key+"="+value)
	}
	if e = os.WriteFile(filepath.Join(root, "console.log"), []byte(strings.Join(lines, "\n")), 0600); e != nil {
		t.Fatal(e)
	}
	log, e := p.runLog(id, 200)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range secrets {
		if strings.Contains(str(log["text"]), secret) {
			t.Error("log exposes sensitive value")
		}
	}
	if !strings.Contains(str(log["text"]), "model=visible-model threads=8") {
		t.Fatal("log hides normal values", log)
	}
	internal, e := p.get("runs", id)
	if e != nil {
		t.Fatal(e)
	}
	for key, value := range secrets {
		if obj(internal["properties"])[key] != value {
			t.Errorf("projection changed internal secret %s", key)
		}
	}
	var rawAfter string
	if e = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&rawAfter); e != nil {
		t.Fatal(e)
	}
	if rawBefore != rawAfter {
		t.Fatal("public projection rewrote stored historical fields")
	}
}

func TestMySQLRunLogMultilineCookieRedaction(t *testing.T) {
	p, _ := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "test-cookie-logs"
	p.Config.ArchiveAccessKey = "test-access"
	p.Config.ArchiveSecretKey = "test-secret"
	_, call := workspaceHTTP(t, p)
	boundaryCookie := "session=" + strings.Repeat("A", 500) + "\nsid=byte-private"
	boundaryLog := boundaryCookie + "\n" + strings.Repeat("x", 256*1024-100) + "\nmodel=visible-model\n"
	for _, tc := range []struct {
		name, value, text string
		tail              int
		archived          bool
		want              string
	}{
		{"lf-tail", "session=first\nsid=private", "visible model=llm threads=8\nsession=first\nsid=private\n", 1, false, "***"},
		{"crlf-normalized", "session=first\r\nsid=private", "visible model=llm threads=8\nsession=first\nsid=private\n", 200, false, "visible model=llm threads=8\n***"},
		{"crlf-tail", "session=first\r\nsid=private", "visible model=llm threads=8\r\nsession=first\r\nsid=private\r\n", 1, true, "***"},
		{"local-byte-boundary", boundaryCookie, boundaryLog, 2000, false, ""},
		{"archive-byte-boundary", boundaryCookie, boundaryLog, 2000, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			props := Obj{"cookie": tc.value, "cookie_blank": " \r\n\t", "password_mask": "*", "model": "visible-model"}
			id := seedCookieLogFixture(t, p, props, tc.text, tc.archived)
			code, result := call("GET", fmt.Sprintf("/api/runs/%s/log?tail=%d", id, tc.tail), nil)
			if code != 200 {
				t.Fatal(code, result)
			}
			text := str(result["text"])
			if strings.Contains(text, "sid=private") || strings.Contains(text, "sid=byte-private") || strings.Contains(text, "AAAA") || strings.Contains(text, "session=first") {
				t.Error("bounded public log exposes multiline Cookie content")
			}
			if tc.want != "" && text != tc.want {
				t.Fatalf("redacted log got %q, want %q", text, tc.want)
			}
			if tc.want == "" && !strings.Contains(text, "model=visible-model") {
				t.Error("byte-boundary handling dropped complete normal log lines")
			}
		})
	}
	// Mask containing values before shorter values, regardless of map iteration.
	for i := 0; i < 12; i++ {
		id := seedCookieLogFixture(t, p, Obj{"cookie": "sid=foo", "http_cookie": "sid=foo; auth=private"}, "visible model=llm\nsid=foo; auth=private\n", false)
		result, e := p.runLog(id, 200)
		if e != nil {
			t.Fatal(e)
		}
		if str(result["text"]) != "visible model=llm\n***" {
			t.Fatal("overlapping Cookie values were only partly masked", result)
		}
	}
}

func seedCookieLogFixture(t *testing.T, p *Platform, props Obj, text string, archived bool) string {
	t.Helper()
	id := newID("run-cookie-log-")
	frozen := Obj{"id": id, "name": "cookie log fixture", "project_id": p.projectID(), "status": "STOPPED", "created_at": now(), "properties": props}
	if archived {
		frozen["execution_mode"] = "redis"
		store, e := p.objectStore()
		if e != nil {
			t.Fatal(e)
		}
		ref, e := store.Put(context.Background(), "runs/"+id+"/console.log", strings.NewReader(text), int64(len(text)), digest([]byte(text)), "text/plain")
		if e != nil {
			t.Fatal(e)
		}
		ref.Name = "console.log"
		frozen["raw_archive"] = Obj{"files": []any{dtoObject(ref)}}
	} else {
		root := filepath.Join(p.Config.ResultsDir, id, "raw")
		if e := os.MkdirAll(root, 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(root, "console.log"), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	stored, e := p.runForStorage(frozen)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("INSERT INTO runs(id,status,created_at,data,project_id) VALUES(?,?,?,?,?)", id, "STOPPED", frozen["created_at"], string(encoded(stored)), p.projectID()); e != nil {
		t.Fatal(e)
	}
	return id
}
