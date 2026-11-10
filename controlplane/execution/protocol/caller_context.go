package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	CallerIPHeader        = "X-PressureCooker-Caller-IP"
	CallerTimeHeader      = "X-PressureCooker-Caller-Time"
	CallerSignatureHeader = "X-PressureCooker-Caller-Signature"
)

func callerIP(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return "", errors.New("invalid caller context")
	}
	return ip.Unmap().String(), nil
}

func callerPayload(r *http.Request, ip, stamp string) (string, error) {
	if r == nil || r.URL == nil || r.Method == "" {
		return "", errors.New("invalid caller context")
	}
	uri, project := r.URL.RequestURI(), r.Header.Get("X-Project-ID")
	if strings.ContainsAny(r.Method+uri+project, "\r\n") {
		return "", errors.New("invalid caller context")
	}
	return "pressurecooker-caller-v1\n" + ip + "\n" + stamp + "\n" + r.Method + "\n" + uri + "\n" + project, nil
}

func callerSignature(payload, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignCallerContext attributes an internal request to the MCP HTTP peer. An
// empty IP deliberately means unavailable; it must not become the manager peer.
// This metadata does not authenticate the request or grant project access.
func SignCallerContext(r *http.Request, ip, token string, at time.Time) error {
	if token == "" || at.UnixMilli() <= 0 {
		return errors.New("invalid caller context")
	}
	ip, err := callerIP(ip)
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(at.UnixMilli(), 10)
	payload, err := callerPayload(r, ip, stamp)
	if err != nil {
		return err
	}
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for key := range r.Header {
		if strings.EqualFold(key, CallerIPHeader) || strings.EqualFold(key, CallerTimeHeader) || strings.EqualFold(key, CallerSignatureHeader) {
			delete(r.Header, key)
		}
	}
	r.Header.Set(CallerIPHeader, ip)
	r.Header.Set(CallerTimeHeader, stamp)
	r.Header.Set(CallerSignatureHeader, callerSignature(payload, token))
	return nil
}

// VerifyCallerContext checks one complete metadata triplet, including its
// method, escaped request URI, project and inclusive two-minute clock window.
// The caller must first require ordinary platform-admin Bearer authentication.
func VerifyCallerContext(r *http.Request, token string, at time.Time) (string, error) {
	invalid := func() (string, error) { return "", errors.New("invalid caller context") }
	if r == nil || token == "" || at.UnixMilli() <= 0 {
		return invalid()
	}
	values := make(map[string][]string, 3)
	for key, entries := range r.Header {
		for _, name := range []string{CallerIPHeader, CallerTimeHeader, CallerSignatureHeader} {
			if strings.EqualFold(key, name) {
				values[name] = append(values[name], entries...)
			}
		}
	}
	for _, name := range []string{CallerIPHeader, CallerTimeHeader, CallerSignatureHeader} {
		if len(values[name]) != 1 {
			return invalid()
		}
	}
	ip, err := callerIP(values[CallerIPHeader][0])
	if err != nil {
		return invalid()
	}
	stamp := values[CallerTimeHeader][0]
	ms, err := strconv.ParseInt(stamp, 10, 64)
	now := at.UnixMilli()
	if err != nil || ms <= 0 || strconv.FormatInt(ms, 10) != stamp || ms > now && ms-now > 120000 || ms < now && now-ms > 120000 {
		return invalid()
	}
	payload, err := callerPayload(r, ip, stamp)
	if err != nil || !hmac.Equal([]byte(values[CallerSignatureHeader][0]), []byte(callerSignature(payload, token))) {
		return invalid()
	}
	return ip, nil
}
