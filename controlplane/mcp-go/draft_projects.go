package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var projectPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)

// The official Node server retains its private authoring format. This index is
// the only authority for exposing those drafts through our project-scoped MCP.
// Existing drafts are assigned to the default project once. Later unindexed
// files remain inaccessible, including orphans from a failed creation.
type draftProjects struct {
	mu     sync.RWMutex
	path   string
	owners map[string]string
}
type draftProjectIndex struct {
	Version int               `json:"version"`
	Owners  map[string]string `json:"owners"`
}

func openDraftProjects(ctx context.Context, upstream upstreamCaller, workspace string) (*draftProjects, error) {
	index := &draftProjects{path: filepath.Join(workspace, "draft-projects.json"), owners: map[string]string{}}
	raw, err := os.ReadFile(index.path)
	if err == nil {
		var stored draftProjectIndex
		if json.Unmarshal(raw, &stored) != nil || stored.Version != 1 || stored.Owners == nil {
			return nil, errors.New("Draft project index is invalid")
		}
		for id, project := range stored.Owners {
			if !draftPattern.MatchString(id) || !projectPattern.MatchString(project) {
				return nil, errors.New("Draft project index is invalid")
			}
		}
		index.owners = stored.Owners
		return index, nil
	}
	if !os.IsNotExist(err) {
		return nil, errors.New("Draft project index is unavailable")
	}
	value, err := upstreamValue(ctx, upstream, "list_test_plans", map[string]any{})
	if err != nil {
		return nil, err
	}
	plans, ok := value.([]any)
	if !ok {
		return nil, errors.New("Upstream draft list is invalid")
	}
	for _, value := range plans {
		plan, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("Upstream draft list is invalid")
		}
		id, ok := plan["planId"].(string)
		if !ok || !draftPattern.MatchString(id) {
			return nil, errors.New("Upstream draft list is invalid")
		}
		index.owners[id] = "project-default"
	}
	if err = index.persist(index.owners); err != nil {
		return nil, err
	}
	return index, nil
}
func (d *draftProjects) persist(owners map[string]string) error {
	raw, err := json.Marshal(draftProjectIndex{Version: 1, Owners: owners})
	if err != nil {
		return errors.New("Cannot encode draft project index")
	}
	file, err := os.CreateTemp(filepath.Dir(d.path), ".draft-projects-*")
	if err != nil {
		return errors.New("Cannot persist draft project index")
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("Cannot persist draft project index")
	}
	if err = os.Rename(temp, d.path); err != nil {
		return errors.New("Cannot persist draft project index")
	}
	return nil
}
func (d *draftProjects) owns(project, id string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.owners[id] == project
}
func (d *draftProjects) bind(project, id string) error {
	if !draftPattern.MatchString(id) {
		return errors.New("Upstream draft has an invalid ID")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.owners[id]; exists {
		return errors.New("Upstream draft ID already exists")
	}
	owners := map[string]string{}
	for k, v := range d.owners {
		owners[k] = v
	}
	owners[id] = project
	if err := d.persist(owners); err != nil {
		return err
	}
	d.owners = owners
	return nil
}

// The local index proves draft ownership, not whether a project still exists or
// permits writes. Recheck the manager on every call so archiving takes effect
// without restarting the authoring subprocess or rewriting its private files.
func (c managerClient) requireAuthoringProject(ctx context.Context, name string, args map[string]any) error {
	project, ok := args["projectId"].(string)
	if !ok || !projectPattern.MatchString(project) {
		return errors.New("Explicit valid projectId is required")
	}
	current, err := c.requestProject(ctx, http.MethodGet, "/api/projects/"+url.PathEscape(project), nil, "", project)
	if err != nil {
		return err
	}
	archived, valid := current["archived"].(bool)
	if current["id"] != project || !valid {
		return errors.New("Manager project state is invalid")
	}
	switch name {
	case "list_test_plans", "get_test_plan", "get_test_plan_xml":
		return nil
	default:
		if archived {
			return errors.New("Project is archived; draft authoring is read-only")
		}
		return nil
	}
}

func (d *draftProjects) call(ctx context.Context, upstream upstreamCaller, name string, args map[string]any, roots []string) (any, error) {
	if !allowedAuthoring(name) {
		return nil, errors.New("Unknown authoring tool")
	}
	project, ok := args["projectId"].(string)
	if !ok || !projectPattern.MatchString(project) {
		return nil, errors.New("Explicit valid projectId is required")
	}
	privateArgs := map[string]any{}
	for k, v := range args {
		if k != "projectId" {
			privateArgs[k] = v
		}
	}
	if name == "import_test_plan" {
		if len(roots) == 0 {
			return nil, errors.New("MCP workspace is required")
		}
		// Shared manager snapshots are content-addressed across projects. Import
		// only explicitly staged project files; manager JMX uses upload/download.
		workspace, err := filepath.EvalSymlinks(roots[0])
		if err != nil {
			return nil, errors.New("MCP workspace is unavailable")
		}
		staging := filepath.Join(workspace, "imports", project)
		resolved, err := filepath.EvalSymlinks(staging)
		if err != nil || resolved != staging {
			return nil, errors.New("Import staging directory must belong to this project without symbolic links")
		}
		roots = []string{staging}
	}
	if err := validateAuthoring(name, privateArgs, roots); err != nil {
		return nil, err
	}
	if name != "create_test_plan" && name != "import_test_plan" && name != "list_test_plans" {
		id, ok := privateArgs["planId"].(string)
		if !ok || !d.owns(project, id) {
			return nil, errors.New("Draft not found in this project")
		}
	}
	value, err := upstreamValue(ctx, upstream, name, privateArgs)
	if err != nil {
		return nil, err
	}
	if name == "list_test_plans" {
		plans, ok := value.([]any)
		if !ok {
			return nil, errors.New("Upstream draft list is invalid")
		}
		filtered := []any{}
		for _, item := range plans {
			if plan, ok := item.(map[string]any); ok {
				if id, ok := plan["planId"].(string); ok && d.owns(project, id) {
					filtered = append(filtered, plan)
				}
			}
		}
		return filtered, nil
	}
	if name == "create_test_plan" || name == "import_test_plan" {
		plan, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("Upstream draft is invalid")
		}
		id, ok := plan["planId"].(string)
		if !ok {
			return nil, errors.New("Upstream draft has no ID")
		}
		if err := d.bind(project, id); err != nil {
			return nil, err
		}
	}
	return value, nil
}
func scopedAuthoringSchema(tool *mcp.Tool) (map[string]any, error) {
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil, errors.New("Cannot encode authoring schema")
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, errors.New("Invalid authoring schema")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	properties["projectId"] = projectSchema()
	required, _ := schema["required"].([]any)
	schema["required"] = append(required, "projectId")
	return schema, nil
}
