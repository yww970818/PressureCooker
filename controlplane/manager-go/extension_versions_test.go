package main

import (
	"context"
	"testing"
)

func TestExtensionVersionsPinnedAndDeletionInert(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	created, err := p.createInterfacePlugin(ctx, Obj{"revision": 0, "definition": customConfigFixture(), "installed": true})
	if err != nil {
		t.Fatal(err)
	}
	id := str(created["created_id"])
	d := customConfigFixture()
	d["id"] = id
	d["version"] = "2.0.0"
	d["name"] = "new name"
	if _, err = p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 1, "definition": d}); err != nil {
		t.Fatal(err)
	}
	old, err := p.extensionVersion(ctx, "interface_plugins", id, "1.0.0")
	if err != nil || obj(old["definition"])["name"] == "new name" {
		t.Fatal("pinned version changed", err)
	}
	current, err := p.getInterfacePluginDefinition(ctx, id)
	if err != nil || obj(current["definition"])["version"] != "2.0.0" {
		t.Fatal(current, err)
	}
	if _, err = p.extensionVersion(ctx, "workflow_extensions", id, "1.0.0"); err == nil {
		t.Fatal("extension kind crossed")
	}
	if _, err = p.deleteInterfacePlugin(ctx, id, Obj{"revision": 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.extensionVersion(ctx, "interface_plugins", id, "1.0.0"); err == nil {
		t.Fatal("deleted extension still active")
	}
	var count int
	if err = p.DB.QueryRow("SELECT COUNT(*) FROM extension_versions WHERE extension_id=?", id).Scan(&count); err != nil || count != 2 {
		t.Fatal("historical definitions lost", count, err)
	}
}

func TestExtensionRestoreRejectsCurrentRegistryDefinitionMismatch(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	created, err := p.createInterfacePlugin(ctx, Obj{"revision": 0, "definition": customConfigFixture(), "installed": true})
	if err != nil {
		t.Fatal(err)
	}
	id := str(created["created_id"])
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	row := obj(arr(state["extension_registries"])[0])
	registry, err := decode([]byte(str(row["data"])))
	if err != nil {
		t.Fatal(err)
	}
	obj(obj(registry["definitions"])[id])["payload"] = "invalid-ciphertext"
	row["data"] = string(encoded(registry))
	dest := emptyWorkspacePlatform(t, p, t.TempDir())
	if err = dest.importState(state); err == nil {
		t.Fatal("corrupt current registry accepted despite intact immutable version")
	}
}

func TestExtensionRestoreAcceptsExplicitRollbackToImmutableVersion(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	original := customConfigFixture()
	created, err := p.createInterfacePlugin(ctx, Obj{"revision": 0, "definition": original, "installed": true})
	if err != nil {
		t.Fatal(err)
	}
	id := str(created["created_id"])
	newer := clone(original)
	newer["id"] = id
	newer["version"] = "2.0.0"
	newer["name"] = "New"
	if _, err = p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 1, "definition": newer}); err != nil {
		t.Fatal(err)
	}
	original["id"] = id
	if _, err = p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 2, "definition": original}); err != nil {
		t.Fatal(err)
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	dest := emptyWorkspacePlatform(t, p, t.TempDir())
	if err = dest.importState(state); err != nil {
		t.Fatal("valid rollback cannot restore", err)
	}
}
