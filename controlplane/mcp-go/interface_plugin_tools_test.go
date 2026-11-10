package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestInterfacePluginToolsUseScopedCatalogAndCASAPI(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		args, want         map[string]any
	}{
		{"list_interface_plugins", "GET", "/api/interface-plugins", map[string]any{}, nil},
		{"list_interface_protocols", "GET", "/api/interface-protocols", map[string]any{}, nil},
		{"get_interface_plugin", "GET", "/api/interface-plugins/custom-one/definition", map[string]any{"pluginId": "custom-one"}, nil},
		{"create_interface_plugin", "POST", "/api/interface-plugins", map[string]any{"revision": 0, "definition": customPluginToolDefinition(), "installed": true}, map[string]any{"revision": float64(0), "definition": customPluginToolDefinition(), "installed": true}},
		{"update_interface_plugin", "PUT", "/api/interface-plugins/custom-one/definition", map[string]any{"pluginId": "custom-one", "revision": 2, "definition": customPluginToolDefinition()}, map[string]any{"revision": float64(2), "definition": customPluginToolDefinition()}},
		{"delete_interface_plugin", "DELETE", "/api/interface-plugins/custom-one", map[string]any{"pluginId": "custom-one", "revision": 3}, map[string]any{"revision": float64(3)}},
		{"install_interface_plugin", "PUT", "/api/interface-plugins/custom-one", map[string]any{"pluginId": "custom-one", "revision": 0}, map[string]any{"revision": float64(0), "installed": true}},
		{"uninstall_interface_plugin", "PUT", "/api/interface-plugins/custom-one", map[string]any{"pluginId": "custom-one", "revision": 7}, map[string]any{"revision": float64(7), "installed": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path || r.Header.Get("X-Project-ID") != "project-one" {
					t.Errorf("wrong scoped request: %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if r.ContentLength > 0 {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
				}
				if !reflect.DeepEqual(body, tc.want) {
					t.Errorf("plugin mutation %#v, want %#v", body, tc.want)
				}
				io.WriteString(w, `{"project_id":"project-one","revision":1,"items":[{"id":"custom-one","renderer":"sandbox-code","installed":true}]}`)
			})
			tc.args["projectId"] = "project-one"
			value, err := m.call(context.Background(), tc.name, tc.args)
			if err != nil || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if value.(map[string]any)["project_id"] != "project-one" || len(value.(map[string]any)["items"].([]any)) != 1 {
				t.Fatal("catalog result lost", value)
			}
		})
	}
}

func customPluginToolDefinition() map[string]any {
	return map[string]any{"kind": "code", "name": "Custom", "version": "1.0.0", "protocols": []any{"websocket"}, "code": map[string]any{"language": "javascript", "source": "function parse(frame){return [{kind:'text',text:frame.data}]}"}}
}

func TestCustomInterfacePluginToolsRejectUnknownCapabilitiesAndMissingCAS(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create_interface_plugin", map[string]any{"revision": 0, "definition": customPluginToolDefinition()}},
		{"create_interface_plugin", map[string]any{"revision": 0, "installed": true, "definition": map[string]any{"kind": "code", "name": "Unsafe", "version": "1.0.0", "protocols": []any{"websocket"}, "url": "https://example.invalid/plugin.js"}}},
		{"update_interface_plugin", map[string]any{"pluginId": "custom-one", "definition": customPluginToolDefinition()}},
		{"delete_interface_plugin", map[string]any{"pluginId": "custom-one"}},
		{"get_interface_plugin", map[string]any{"pluginId": "../other"}},
	} {
		tc.args["projectId"] = "project-one"
		if _, e := m.call(context.Background(), tc.name, tc.args); e == nil {
			t.Fatal("invalid tool arguments accepted", tc.name)
		}
	}
	if calls != 0 {
		t.Fatal("invalid request reached manager")
	}
}

func TestInterfacePluginToolsRejectMissingScopeRevisionAndExecutableInput(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_interface_plugins", map[string]any{}},
		{"install_interface_plugin", map[string]any{"pluginId": "custom-one", "revision": 0}},
		{"install_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "custom-one"}},
		{"uninstall_interface_plugin", map[string]any{"projectId": "project-one", "revision": 0}},
		{"install_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "../custom-one", "revision": 0}},
		{"install_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "custom-one", "revision": -1}},
		{"uninstall_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "custom-one", "revision": 0.5}},
		{"install_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "custom-one", "revision": "0"}},
		{"install_interface_plugin", map[string]any{"projectId": "project-one", "pluginId": "custom-one", "revision": 0, "url": "https://example.invalid/plugin.js"}},
	} {
		_, err := m.call(context.Background(), tc.name, tc.args)
		if err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for ") {
			t.Fatalf("invalid %s args accepted: %v", tc.name, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid plugin arguments reached HTTP")
	}
}
