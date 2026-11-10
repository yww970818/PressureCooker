package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"pressurecooker/execution/protocol"
)

// Every Core currently observes the complete configured pool. Keep startup
// inventory within the same bounded registration protocol as its response.
func validateNodeInventories(controllers, workers map[string]string) error {
	if len(workers) > protocol.MaxNodeReportWorkers {
		return fmt.Errorf("configured worker inventory exceeds protocol limit %d", protocol.MaxNodeReportWorkers)
	}
	for _, inventory := range []map[string]string{controllers, workers} {
		for id := range inventory {
			if !protocol.ValidID(id) {
				return fmt.Errorf("invalid configured node identity")
			}
		}
	}
	return nil
}

// A deployment may explicitly define its initial mapping. Saved cluster rows
// always take precedence; this never rewrites existing or frozen configuration.
func parseDefaultClusterWorkers(raw string, explicit bool, controllers, workers map[string]string) (map[string][]string, error) {
	groups := map[string][]string{}
	if !explicit {
		return groups, nil
	}
	if strings.TrimSpace(raw) == "null" || json.Unmarshal([]byte(raw), &groups) != nil || groups == nil {
		return nil, fmt.Errorf("invalid PRESSURECOOKER_CLUSTER_WORKERS")
	}
	for core, members := range groups {
		if _, ok := controllers[core]; !ok || len(members) > protocol.MaxNodeReportWorkers {
			return nil, fmt.Errorf("invalid PRESSURECOOKER_CLUSTER_WORKERS")
		}
		seen := map[string]bool{}
		for _, id := range members {
			if _, ok := workers[id]; !ok || seen[id] || !protocol.ValidID(id) {
				return nil, fmt.Errorf("invalid PRESSURECOOKER_CLUSTER_WORKERS")
			}
			seen[id] = true
		}
	}
	return groups, nil
}

func (p *Platform) nodeDisplayName(id, role string) string {
	if role != "controller" {
		return id
	}
	for index, known := range sortedKeys(p.Config.ControllerURLs) {
		if known == id {
			return fmt.Sprintf("PressureCooker-Core-%d", index+1)
		}
	}
	return "PressureCooker-Core · " + id
}
