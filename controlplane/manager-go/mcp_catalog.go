package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const mcpCatalogLimit = 2 * 1024 * 1024
const mcpCatalogTimeout = 5 * time.Second

var errMCPCatalog = errors.New("MCP catalog unavailable")
var mcpVersionRE = regexp.MustCompile(`^20[0-9]{2}-[0-9]{2}-[0-9]{2}$`)

type mcpCatalogClient struct {
	http                              *http.Client
	endpoint, token, version, session string
	budget                            int
	nextID                            int
}

func newMCPCatalogClient(c Config) *mcpCatalogClient {
	port := c.MCPLocalPort
	if port == 0 {
		port = 8081
	}
	endpoint := "http://127.0.0.1:" + fmt.Sprint(port) + "/mcp"
	if c.mcpCatalogTestURL != "" {
		endpoint = c.mcpCatalogTestURL
	}
	dialer := &net.Dialer{Timeout: mcpCatalogTimeout}
	transport := &http.Transport{DialContext: dialer.DialContext, ResponseHeaderTimeout: mcpCatalogTimeout, MaxResponseHeaderBytes: 64 * 1024, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: mcpCatalogTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &mcpCatalogClient{http: client, endpoint: endpoint, token: c.AdminToken, version: "2025-11-25", budget: mcpCatalogLimit}
}
func (c *mcpCatalogClient) call(ctx context.Context, method string, params Obj, notification bool) (Obj, error) {
	message := Obj{"jsonrpc": "2.0", "method": method, "params": params}
	id := 0
	if !notification {
		c.nextID++
		id = c.nextID
		message["id"] = id
	}
	request, e := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(encoded(message)))
	if e != nil {
		return nil, errMCPCatalog
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("MCP-Protocol-Version", c.version)
	if c.session != "" {
		request.Header.Set("Mcp-Session-Id", c.session)
	}
	response, e := c.http.Do(request)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errMCPCatalog
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		if len(session) > 2048 {
			return nil, errMCPCatalog
		}
		c.session = session
	}
	if notification {
		raw, e := io.ReadAll(io.LimitReader(response.Body, int64(c.budget)+1))
		c.budget -= len(raw)
		if e != nil || c.budget < 0 {
			return nil, errMCPCatalog
		}
		return Obj{}, nil
	}
	return c.readResult(response, id)
}
func (c *mcpCatalogClient) readResult(response *http.Response, id int) (Obj, error) {
	reader := &io.LimitedReader{R: response.Body, N: int64(c.budget) + 1}
	before := reader.N
	defer func() { c.budget -= int(before - reader.N) }()
	parse := func(raw []byte) (Obj, bool, error) {
		if !json.Valid(raw) {
			return nil, false, errMCPCatalog
		}
		message, e := decode(raw)
		if e != nil || str(message["jsonrpc"]) != "2.0" {
			return nil, false, errMCPCatalog
		}
		if int(number(message["id"])) != id {
			return nil, false, nil
		}
		if message["error"] != nil {
			return nil, false, errMCPCatalog
		}
		result, ok := message["result"].(map[string]any)
		if !ok {
			return nil, false, errMCPCatalog
		}
		return result, true, nil
	}
	if strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), mcpCatalogLimit+1)
		var event strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				if event.Len() > 0 {
					event.WriteByte('\n')
				}
				event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
				if event.Len() > mcpCatalogLimit {
					return nil, errMCPCatalog
				}
			} else if line == "" && event.Len() > 0 {
				result, found, e := parse([]byte(event.String()))
				event.Reset()
				if e != nil {
					return nil, e
				}
				if found {
					if reader.N <= 0 {
						return nil, errMCPCatalog
					}
					return result, nil
				}
			}
		}
		return nil, errMCPCatalog
	}
	raw, e := io.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	if reader.N <= 0 {
		return nil, errMCPCatalog
	}
	result, found, e := parse(raw)
	if e != nil || !found {
		return nil, errMCPCatalog
	}
	return result, nil
}
func publicMCPEndpoint(c Config) string {
	value := c.MCPPublicURL
	if value == "" {
		port := c.MCPLocalPort
		if port == 0 {
			port = 8081
		}
		value = "http://127.0.0.1:" + fmt.Sprint(port) + "/mcp"
	}
	u, e := url.Parse(value)
	if e != nil || !contains([]string{"http", "https"}, u.Scheme) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "/mcp"
	}
	return value
}
func redactCatalog(value any, token string) any {
	switch v := value.(type) {
	case string:
		if token != "" {
			return strings.ReplaceAll(v, token, "***")
		}
		return v
	case map[string]any:
		out := Obj{}
		for k, item := range v {
			safeKey := k
			if token != "" {
				safeKey = strings.ReplaceAll(k, token, "***")
			}
			out[safeKey] = redactCatalog(item, token)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactCatalog(item, token)
		}
		return out
	default:
		return v
	}
}
func (p *Platform) mcpTools(ctx context.Context) Obj {
	result := Obj{"available": false, "endpoint": publicMCPEndpoint(p.Config), "protocol": "streamable-http", "authentication": "bearer", "tools": []any{}, "total": 0}
	unavailable := func(e error) Obj {
		message := "MCP工具目录暂时不可用，请稍后重试。"
		var timeout net.Error
		if errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &timeout) && timeout.Timeout()) {
			message = "MCP工具目录读取超时，请稍后重试。"
		}
		result["error"] = message
		return obj(redactCatalog(result, p.Config.AdminToken))
	}
	ctx, cancel := context.WithTimeout(ctx, mcpCatalogTimeout)
	defer cancel()
	client := newMCPCatalogClient(p.Config)
	defer client.http.CloseIdleConnections()
	initialized, e := client.call(ctx, "initialize", Obj{"protocolVersion": client.version, "capabilities": Obj{}, "clientInfo": Obj{"name": "jmeter-manager-catalog", "version": "1.0.0"}}, false)
	if e != nil {
		return unavailable(e)
	}
	version := str(initialized["protocolVersion"])
	if !mcpVersionRE.MatchString(version) {
		return unavailable(errMCPCatalog)
	}
	client.version = version
	if _, e = client.call(ctx, "notifications/initialized", Obj{}, true); e != nil {
		return unavailable(e)
	}
	tools := []any{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 32; page++ {
		params := Obj{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		listed, e := client.call(ctx, "tools/list", params, false)
		if e != nil {
			return unavailable(e)
		}
		items, ok := listed["tools"].([]any)
		if !ok {
			return unavailable(errMCPCatalog)
		}
		for _, item := range items {
			tool, ok := item.(map[string]any)
			if !ok || str(tool["name"]) == "" {
				return unavailable(errMCPCatalog)
			}
			if _, ok = tool["inputSchema"].(map[string]any); !ok {
				return unavailable(errMCPCatalog)
			}
			tools = append(tools, tool)
		}
		if len(tools) > 2000 {
			return unavailable(errMCPCatalog)
		}
		next, ok := listed["nextCursor"]
		if !ok || next == nil || next == "" {
			result["available"] = true
			result["tools"] = tools
			result["total"] = len(tools)
			server := obj(initialized["serverInfo"])
			if name, version := str(server["name"]), str(server["version"]); name != "" && version != "" {
				result["server"] = Obj{"name": name, "version": version}
			}
			safe := obj(redactCatalog(result, p.Config.AdminToken))
			if len(encoded(safe)) > mcpCatalogLimit {
				return unavailable(errMCPCatalog)
			}
			return safe
		}
		cursor, ok = next.(string)
		if !ok || len(cursor) > 4096 || seen[cursor] {
			return unavailable(errMCPCatalog)
		}
		seen[cursor] = true
	}
	return unavailable(errMCPCatalog)
}
