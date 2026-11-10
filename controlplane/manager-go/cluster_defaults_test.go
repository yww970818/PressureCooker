package main

import (
	"reflect"
	"testing"
)

func TestCoreDefaultClusterGroupsPartitionTwoAndFour(t *testing.T) {
	controllers := map[string]string{"controller-a": "http://a:8082", "controller-b": "http://b:8082"}
	workers := map[string]string{"worker-1": "http://w1:8082", "worker-2": "http://w2:8082", "worker-3": "http://w3:8082", "worker-4": "http://w4:8082", "worker-5": "http://w5:8082", "worker-6": "http://w6:8082"}
	groups, err := parseDefaultClusterWorkers(`{"controller-a":["worker-1","worker-2"],"controller-b":["worker-3","worker-4","worker-5","worker-6"]}`, true, controllers, workers)
	if err != nil || !reflect.DeepEqual(groups["controller-a"], []string{"worker-1", "worker-2"}) || !reflect.DeepEqual(groups["controller-b"], []string{"worker-3", "worker-4", "worker-5", "worker-6"}) {
		t.Fatal(groups, err)
	}
	explicit, err := parseDefaultClusterWorkers(`{"controller-b":["worker-1"]}`, true, controllers, workers)
	if err != nil || !reflect.DeepEqual(explicit, map[string][]string{"controller-b": {"worker-1"}}) {
		t.Fatal(explicit, err)
	}
	legacy, err := parseDefaultClusterWorkers(`{}`, true, controllers, workers)
	if err != nil || len(legacy) != 0 {
		t.Fatal(legacy, err)
	}
	for _, raw := range []string{`null`, `[]`, `{"missing":["worker-1"]}`, `{"controller-a":["missing"]}`, `{"controller-a":["worker-1","worker-1"]}`, `{"controller-a":"worker-1"}`} {
		if _, err := parseDefaultClusterWorkers(raw, true, controllers, workers); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestCoreDefaultGroupsDoNotRepartitionCustomInventories(t *testing.T) {
	groups, err := parseDefaultClusterWorkers("", false, map[string]string{"custom": "http://c"}, map[string]string{"worker-1": "http://w1"})
	if err != nil || len(groups) != 0 {
		t.Fatal(groups, err)
	}
}

func TestMySQLCoreInitialLayoutAndSavedOverridesPreserveRunSnapshots(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.DefaultClusterWorkers, _ = parseDefaultClusterWorkers(`{"controller-a":["worker-1","worker-2"],"controller-b":["worker-3","worker-4","worker-5","worker-6"]}`, true, p.Config.ControllerURLs, p.Config.WorkerURLs)
	groups, err := p.clusterConfigurations()
	if err != nil {
		t.Fatal(err)
	}
	if obj(groups[0])["name"] != "PressureCooker-Core-1" || len(arr(obj(groups[0])["worker_ids"])) != 2 || len(arr(obj(groups[1])["worker_ids"])) != 4 {
		t.Fatal(groups)
	}
	run := runFixture(t, p, plan, "core-layout")
	snapshot := clone(obj(run["cluster_snapshot"]))
	run["status"] = "STOPPED"
	if err = p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); err != nil {
		t.Fatal(err)
	}
	updated, err := p.updateCluster(clusterID("controller-a"), Obj{"revision": 0, "name": "PressureCooker-Core-1", "worker_ids": []any{"worker-2"}})
	if err != nil || !boolean(updated["configured"]) {
		t.Fatal(updated, err)
	}
	fresh, err := p.get("runs", str(run["id"]))
	if err != nil || !reflect.DeepEqual(obj(fresh["cluster_snapshot"]), snapshot) {
		t.Fatal(fresh, err)
	}
	groups, err = p.clusterConfigurations()
	if err != nil || len(arr(obj(groups[0])["worker_ids"])) != 1 {
		t.Fatal(groups, err)
	}
}
