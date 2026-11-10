package main

import "strings"

const runEnvironmentEnvelope = "environment_envelope"
const runEnvironmentDisplay = "environment_display_envelope"

func runEnvironmentContext(run Obj) string {
	return str(run["id"]) + "/" + objectProject(run) + "/environment-v1"
}

func (p *Platform) sealRunEnvironmentDisplay(run Obj) (string, error) {
	snapshot := obj(run["environment_snapshot"])
	if len(snapshot) == 0 {
		return "", nil
	}
	display := Obj{}
	for _, key := range []string{"id", "project_id", "name", "revision", "base_host"} {
		display[key] = snapshot[key]
	}
	return p.sealWorkspace("run-display", runEnvironmentContext(run), 1, display)
}

// Keep status, ownership and relationships queryable. Only the environment
// snapshot and its injected properties are encrypted in durable run JSON.
func (p *Platform) runForStorage(run Obj) (Obj, error) {
	stored := clone(run)
	properties := obj(stored["properties"])
	injected := Obj{}
	for key, value := range properties {
		if str(run["execution_mode"]) == "redis" || strings.HasPrefix(key, "jmeter.env.") {
			injected[key] = value
			delete(properties, key)
		}
	}
	if stored["environment_snapshot"] == nil && len(injected) == 0 {
		return stored, nil
	}
	payload := Obj{"environment_snapshot": stored["environment_snapshot"], "properties": injected}
	sealed, e := p.sealWorkspace("runs", runEnvironmentContext(run), 1, payload)
	if e != nil {
		return nil, e
	}
	stored[runEnvironmentEnvelope] = sealed
	if displaySeal, err := p.sealRunEnvironmentDisplay(run); err != nil {
		return nil, err
	} else if displaySeal != "" {
		stored[runEnvironmentDisplay] = displaySeal
	}
	delete(stored, "environment_snapshot")
	return stored, nil
}

func (p *Platform) openStoredRun(run Obj) (Obj, error) {
	raw, exists := run[runEnvironmentEnvelope]
	if !exists {
		return run, nil
	}
	failure := func() (Obj, error) {
		return nil, api(500, "run_environment_decryption_failed", "无法读取任务环境快照，请检查原始会话密钥是否保留")
	}
	sealed, ok := raw.(string)
	if !ok || sealed == "" {
		return failure()
	}
	payload, e := p.openWorkspace("runs", runEnvironmentContext(run), 1, sealed)
	if e != nil {
		return failure()
	}
	injected, ok := payload["properties"].(map[string]any)
	if !ok {
		return failure()
	}
	properties := obj(run["properties"])
	for key, value := range injected {
		if str(run["execution_mode"]) != "redis" && !strings.HasPrefix(key, "jmeter.env.") {
			return failure()
		}
		if _, exists := properties[key]; exists {
			return failure()
		}
		if _, ok := value.(string); !ok {
			return failure()
		}
		properties[key] = value
	}
	snapshot := payload["environment_snapshot"]
	if snapshot != nil {
		data, ok := snapshot.(map[string]any)
		if !ok || objectProject(data) != objectProject(run) || data["id"] != run["environment_id"] {
			return failure()
		}
	}
	run["properties"] = properties
	run["environment_snapshot"] = snapshot
	delete(run, runEnvironmentEnvelope)
	delete(run, runEnvironmentDisplay)
	return run, nil
}
