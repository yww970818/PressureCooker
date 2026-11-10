package main

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"pressurecooker/execution/protocol"
)

type environmentOperation struct {
	clientIP    string
	timestampMS int64
}
type environmentOperationKey struct{}

func newEnvironmentOperation(ip string, at time.Time) environmentOperation {
	address, err := netip.ParseAddr(ip)
	if err != nil || address.Zone() != "" {
		ip = ""
	} else {
		ip = address.Unmap().String()
	}
	return environmentOperation{clientIP: ip, timestampMS: at.UnixMilli()}
}
func withEnvironmentOperation(ctx context.Context, operation environmentOperation) context.Context {
	return context.WithValue(ctx, environmentOperationKey{}, operation)
}
func environmentOperationFromContext(ctx context.Context) environmentOperation {
	if operation, ok := ctx.Value(environmentOperationKey{}).(environmentOperation); ok {
		return operation
	}
	return newEnvironmentOperation("", time.Now())
}
func (op environmentOperation) values() Obj {
	return Obj{"client_ip": op.clientIP, "timestamp_ms": strconv.FormatInt(op.timestampMS, 10)}
}

func callerContextHeader(name string) bool {
	return strings.EqualFold(name, protocol.CallerIPHeader) || strings.EqualFold(name, protocol.CallerTimeHeader) || strings.EqualFold(name, protocol.CallerSignatureHeader)
}
func stripCallerContextHeaders(headers http.Header) {
	for name := range headers {
		if callerContextHeader(name) {
			delete(headers, name)
		}
	}
}
func requestPeerIP(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	return newEnvironmentOperation(address, time.Now()).clientIP
}
func (p *Platform) environmentOperationForRequest(r *http.Request, auth Obj) (environmentOperation, error) {
	at := time.Now()
	ip := requestPeerIP(r.RemoteAddr)
	metadata := false
	for name := range r.Header {
		if callerContextHeader(name) {
			metadata = true
		}
	}
	if metadata {
		authorization := r.Header.Values("Authorization")
		if auth["bearer"] != true || len(authorization) != 1 || !constant(authorization[0], "Bearer "+p.Config.AdminToken) {
			return environmentOperation{}, api(400, "invalid_caller_context", "调用方来源元信息无效")
		}
		var err error
		ip, err = protocol.VerifyCallerContext(r, p.Config.AdminToken, at)
		if err != nil {
			return environmentOperation{}, api(400, "invalid_caller_context", "调用方来源元信息无效")
		}
	}
	return newEnvironmentOperation(ip, at), nil
}
