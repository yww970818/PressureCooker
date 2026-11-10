package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"pressurecooker/execution/protocol"
)

type callerSourceKey struct{}

func withCallerSource(r *http.Request) *http.Request {
	ip := ""
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if address, err := netip.ParseAddr(host); err == nil && address.Zone() == "" {
			ip = address.Unmap().String()
		}
	}
	// Only the HTTP server's observed peer enters this private context. SDK
	// request metadata and user-controlled forwarding/caller headers are ignored.
	return r.WithContext(context.WithValue(r.Context(), callerSourceKey{}, ip))
}

func signManagerCaller(r *http.Request, token string) error {
	ip, _ := r.Context().Value(callerSourceKey{}).(string)
	if err := protocol.SignCallerContext(r, ip, token, time.Now()); err != nil {
		return errors.New("Cannot attribute manager request")
	}
	return nil
}
