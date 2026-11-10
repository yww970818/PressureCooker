package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHistoryImportPreservesGenericWorkerIdentities(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.Config.ArchiveEndpoint = ""
	id, source := newID("history-"), t.TempDir()
	directory := filepath.Join(source, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := Obj{"status": "FAILED", "engines": "worker-1:1099, worker-7:2099,load-east,_engine-b,worker-7:2099,../bad,worker-2:invalid,worker-3:70000"}
	if err := os.WriteFile(filepath.Join(directory, "run.meta"), encoded(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.importHistory(source); err != nil {
		t.Fatal(err)
	}
	run, err := p.get("runs", id)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stringList(run["workers"]), []string{"worker-1", "worker-7", "load-east", "_engine-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("historical worker identities changed: got %v want %v", got, want)
	}
}

func TestHistoryImportCLIRequiresProjectBeforeOpeningDatabase(t *testing.T) {
	t.Setenv("JMETER_MYSQL_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing"))
	for _, args := range [][]string{{"import-history", t.TempDir()}, {"import-history", t.TempDir(), "--project", "../invalid"}} {
		if err := runCLI(args); err == nil || !strings.Contains(err.Error(), "project") {
			t.Fatalf("history import must validate explicit project first: %v", err)
		}
	}
}

func TestHistoryImportHonorsSelectedProject(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "history destination"})
	if err != nil {
		t.Fatal(err)
	}
	source, id := t.TempDir(), newID("history-")
	if err = os.Mkdir(filepath.Join(source, id), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, id, "run.meta"), encoded(Obj{"status": "FAILED"}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = p.forProject(str(project["id"])).importHistory(source); err != nil {
		t.Fatal(err)
	}
	run, err := p.forProject(str(project["id"])).get("runs", id)
	if err != nil || run["project_id"] != project["id"] {
		t.Fatalf("selected project ignored: %v %v", run, err)
	}
}

func TestHistoryImportRejectsMissingAndArchivedProjectsBeforeCopy(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "archived destination"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.projectUpdate(str(project["id"]), Obj{"revision": 1}, true); err != nil {
		t.Fatal(err)
	}
	source, id := t.TempDir(), newID("history-")
	if err = os.Mkdir(filepath.Join(source, id), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, id, "results.jtl"), []byte("synthetic history"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, projectID := range []string{"missing-project", str(project["id"])} {
		if _, err = p.forProject(projectID).importHistory(source); err == nil {
			t.Errorf("history imported into unavailable project %s", projectID)
		}
	}
	if _, err = os.Stat(filepath.Join(p.Config.ResultsDir, id)); !os.IsNotExist(err) {
		t.Fatal("rejected import copied files", err)
	}
}
