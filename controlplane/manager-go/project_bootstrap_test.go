package main

import "testing"

func TestProjectBootstrapKeepsExistingNonDefaultProjectAcrossRestart(t *testing.T) {
	p, plan := mysqlFixture(t)
	keep, e := p.projectCreate(Obj{"name": "retained-generic-project"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("DELETE FROM plans WHERE id=?", plan["id"]); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("DELETE FROM projects WHERE id=?", defaultProjectID); e != nil {
		t.Fatal(e)
	}
	before := string(encoded(keep))
	for i := 0; i < 2; i++ {
		restarted, e := newPlatform(p.Config)
		if e != nil {
			t.Fatal(e)
		}
		var count int
		e = restarted.DB.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
		if e != nil || count != 1 {
			restarted.Close()
			t.Fatalf("startup recreated an unrequested default project: count=%d error=%v", count, e)
		}
		got, e := restarted.projectGetQ(restarted.DB, str(keep["id"]), false)
		restarted.Close()
		if e != nil || string(encoded(got)) != before {
			t.Fatal("retained project changed", e)
		}
	}
}
func TestProjectBootstrapCreatesDefaultOnlyForEmptyDatabase(t *testing.T) {
	p, plan := mysqlFixture(t)
	if _, e := p.DB.Exec("DELETE FROM plans WHERE id=?", plan["id"]); e != nil {
		t.Fatal(e)
	}
	if _, e := p.DB.Exec("DELETE FROM projects"); e != nil {
		t.Fatal(e)
	}
	if e := p.ensureDefaultProject(); e != nil {
		t.Fatal(e)
	}
	if e := p.ensureDefaultProject(); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	if _, e := p.projectGetQ(p.DB, defaultProjectID, false); e != nil {
		t.Fatal(e)
	}
}
