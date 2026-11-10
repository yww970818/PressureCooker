package main

import (
	"encoding/json"
	"errors"
	"strings"

	"pressurecooker/execution/protocol"
)

// controllerRuntimeProperties copies user properties and binds the controller's
// signed assignment. These values travel through the same private -G file as
// the task properties; no caller or CSV may supply this namespace.
func controllerRuntimeProperties(task protocol.Task) (map[string]string, error) {
	values := make(map[string]string, len(task.Properties)+2)
	for key, value := range task.Properties {
		if strings.HasPrefix(strings.ToLower(key), protocol.ControllerRuntimePrefix) {
			return nil, errors.New("controller runtime properties cannot be supplied by the task")
		}
		values[key] = value
	}
	ids := make([]string, 0, len(task.Workers))
	seen := map[string]bool{}
	for _, worker := range task.Workers {
		if !protocol.ValidID(worker.ID) || seen[worker.ID] {
			return nil, errors.New("invalid signed worker assignment")
		}
		seen[worker.ID] = true
		ids = append(ids, worker.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	values[protocol.ControllerWorkersProperty] = string(raw)
	values[protocol.ControllerTaskProperty] = task.TaskID
	return values, nil
}
