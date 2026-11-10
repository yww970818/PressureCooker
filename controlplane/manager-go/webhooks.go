package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var webhookTableColumns = []string{"event_id", "run_id", "target_url", "payload", "status", "attempts", "next_attempt_at", "lease_token", "lease_until", "last_status_code", "last_error", "created_at"}

// Administrator supplied private/intranet endpoints are supported. Errors must
// not echo the address: query parameters may contain receiver credentials.
func normalizeCallbackURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	invalid := func() (string, error) {
		return "", api(400, "invalid_callback_url", "回调地址须为有效的 HTTP/HTTPS URL，最长 2048 字节，不能包含用户信息或片段")
	}
	if len(value) > 2048 || strings.Contains(value, "#") {
		return invalid()
	}
	u, e := url.Parse(value)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return invalid()
	}
	// Request construction additionally rejects malformed hosts/control characters.
	if _, e = http.NewRequest(http.MethodPost, value, nil); e != nil {
		return invalid()
	}
	return value, nil
}

func (p *Platform) webhookDelivery(runID string) (Obj, error) {
	var id, status, lastError string
	var attempts, code int
	var next float64
	e := p.DB.QueryRow("SELECT event_id,status,attempts,last_status_code,last_error,next_attempt_at FROM webhook_deliveries WHERE run_id=?", runID).Scan(&id, &status, &attempts, &code, &lastError, &next)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return publicWebhookDelivery(id, status, attempts, code, lastError, next), nil
}

func publicWebhookDelivery(id, status string, attempts, code int, lastError string, next float64) Obj {
	nextAt := ""
	if next > 0 {
		nextAt = time.UnixMilli(int64(next * 1000)).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	// Only fixed classifications are exposed, even if an imported row contained
	// an unsafe legacy error message.
	switch lastError {
	case "", "http_status", "request_failed", "delivery_interrupted":
	default:
		lastError = "request_failed"
	}
	return Obj{"event_id": id, "status": status, "attempts": attempts, "last_status_code": code, "last_error": lastError, "next_attempt_at": nextAt}
}
