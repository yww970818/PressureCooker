package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Return controlled categories, never raw paths, query strings or error text.
func requestRoute(path string) string {
	if path == "/healthz" || path == "/readyz" {
		return "health"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 1 && parts[0] == "api" {
		if contains([]string{"session", "login", "logout", "projects", "environments", "runs", "plans", "datasets", "pressure-reports", "api-collections", "workflows", "runner", "api-proxy", "mcp", "cluster", "clusters", "diagnostics"}, parts[1]) {
			return parts[1]
		}
		return "api"
	}
	return "frontend"
}

func requestFailure(err error) (int, string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return 504, "request_timeout", "请求处理超时，请稍后重试"
	}
	if errors.Is(err, context.Canceled) {
		return 499, "request_cancelled", "请求已取消"
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status, apiErr.Code, apiErr.Message
	}
	return 500, "internal_error", "Internal server error"
}

func logRequestFailure(r *http.Request, id, code string, err any, started time.Time) {
	method := r.Method
	if !contains([]string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"}, method) {
		method = "OTHER"
	}
	sqlCode := uint16(0)
	if e, ok := err.(error); ok {
		var m *mysql.MySQLError
		if errors.As(e, &m) {
			sqlCode = m.Number
		}
	}
	log.Printf("event=request_failed request_id=%s method=%s route=%s code=%s error_type=%T sql_code=%d elapsed_ms=%d", id, method, requestRoute(r.URL.Path), code, err, sqlCode, time.Since(started).Milliseconds())
}
