package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type config struct {
	address, token, workspace, plans, node, upstream string
	api                                              managerClient
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadConfig() (config, error) {
	tokenFile := env("JMETER_ADMIN_TOKEN_FILE", "/run/platform/admin-token")
	raw, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return config{}, errors.New("MCP authentication token is unavailable or empty")
	}
	workspace, err := filepath.Abs(env("JMETER_MCP_WORKSPACE", "/state/jmeter-mcp"))
	if err != nil {
		return config{}, errors.New("Invalid MCP workspace")
	}
	apiURL := env("JMETER_API_URL", "http://127.0.0.1:8080")
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return config{}, errors.New("Invalid manager API URL")
	}
	publicURL := strings.TrimSpace(env("JMETER_MANAGER_PUBLIC_URL", ""))
	if !validPublicManagerURL(publicURL) {
		return config{}, errors.New("Invalid public manager URL")
	}
	return config{address: net.JoinHostPort(env("JMETER_MCP_HOST", "0.0.0.0"), env("JMETER_MCP_PORT", "8081")), token: strings.TrimSpace(string(raw)), workspace: workspace, plans: env("JMETER_PLANS_DIR", "/plans"), node: env("JMETER_MCP_NODE", "/usr/local/bin/node"), upstream: env("JMETER_MCP_UPSTREAM", "/app/mcp/node_modules/jmeter-mcp-server/dist/index.js"), api: managerClient{baseURL: apiURL, publicURL: publicURL, tokenFile: env("JMETER_MANAGER_TOKEN_FILE", tokenFile)}}, nil
}

func run(ctx context.Context, cfg config, output io.Writer) error {
	if err := os.MkdirAll(cfg.workspace, 0700); err != nil {
		return errors.New("Cannot create MCP workspace")
	}
	childCtx, cancelChild := context.WithCancel(ctx)
	defer cancelChild()
	upstream, err := connectUpstream(childCtx, cfg.node, cfg.upstream, cfg.workspace)
	if err != nil {
		return err
	}
	defer upstream.Close()
	startup, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	proxy, err := newProxyServer(startup, upstream, cfg.api, []string{cfg.workspace, cfg.plans})
	cancelStartup()
	if err != nil {
		return err
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return proxy }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxMCPBody})
	server := &http.Server{Addr: cfg.address, Handler: secureHandler(cfg.token, handler), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	listener, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return errors.New("Cannot listen on MCP port")
	}
	defer server.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	upstreamDone := make(chan error, 1)
	go func() { upstreamDone <- upstream.Wait() }()
	if _, err = fmt.Fprintf(output, "MCP_LISTENING %d\n", listener.Addr().(*net.TCPAddr).Port); err != nil {
		return errors.New("Cannot announce MCP readiness")
	}
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
		return nil
	case <-upstreamDone:
		return errors.New("Official JMeter authoring process exited")
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("MCP HTTP server stopped")
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	cfg, err := loadConfig()
	if err == nil {
		err = run(ctx, cfg, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
