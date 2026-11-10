package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const proxyRequestLimit = 6 * 1024 * 1024
const proxyResponseLimit = 4 * 1024 * 1024

var proxyMetadataIPs = map[string]bool{"100.100.100.200": true, "168.63.129.16": true, "fd00:ec2::254": true}

func safeProxyIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() || address.IsMulticast() || proxyMetadataIPs[address.String()] {
		return false
	}
	if address.Is4() {
		v := address.As4()
		if v[0] == 0 || v[0] >= 224 {
			return false
		}
	}
	if netip.MustParsePrefix("64:ff9b::/96").Contains(address) || netip.MustParsePrefix("64:ff9b:1::/48").Contains(address) {
		return false
	}
	return true
}
func proxyTarget(raw string) (*url.URL, error) {
	target, e := url.Parse(raw)
	if e != nil || !contains([]string{"http", "https"}, target.Scheme) || target.Host == "" || target.Hostname() == "" || target.User != nil || target.Fragment != "" || target.Opaque != "" || strings.ContainsAny(target.Host, "\\%\x00\r\n") {
		return nil, api(400, "invalid_proxy_target", "代理目标必须是无内嵌凭据的HTTP或HTTPS绝对地址")
	}
	if port := target.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, api(400, "invalid_proxy_target", "代理目标端口无效")
		}
	}
	return target, nil
}
func resolveProxyIPs(ctx context.Context, c Config, host string) ([]net.IPAddr, error) {
	var ips []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IPAddr{{IP: literal}}
	} else {
		resolver := net.DefaultResolver.LookupIPAddr
		if c.proxyTestResolver != nil {
			resolver = c.proxyTestResolver
		}
		var e error
		ips, e = resolver(ctx, host)
		if e != nil {
			return nil, api(502, "proxy_dns_failed", "无法解析代理目标地址")
		}
	}
	if len(ips) == 0 || len(ips) > 32 {
		return nil, api(502, "proxy_dns_failed", "代理目标地址数量无效")
	}
	for _, address := range ips {
		if address.Zone != "" || !safeProxyIP(address.IP) {
			return nil, api(403, "proxy_target_forbidden", "该目标属于本机、链路本地、元数据或非单播地址，禁止代理访问")
		}
	}
	return ips, nil
}
func stripHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(key)
	}
}
func proxyRequestHeaders(original http.Header) http.Header {
	out := original.Clone()
	stripHopHeaders(out)
	for key := range out {
		lower := strings.ToLower(key)
		if lower == "cookie" || lower == "x-pressure-protocol" || lower == "x-pressure-serialization" || lower == "x-jmeter-csrf-token" || lower == "x-jmeter-project-id" || lower == "origin" || lower == "referer" || lower == "host" || lower == "content-length" || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "sec-") || strings.HasPrefix(lower, "x-scalar-") {
			out.Del(key)
		}
	}
	for alias, target := range map[string]string{"x-scalar-cookie": "Cookie", "x-scalar-user-agent": "User-Agent", "x-scalar-date": "Date", "x-scalar-dnt": "DNT", "x-scalar-referer": "Referer"} {
		if values := original.Values(alias); len(values) > 0 {
			out[target] = append([]string(nil), values...)
		}
	}
	stripCallerContextHeaders(out)
	return out
}
func (p *Platform) apiProxy(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if !contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, r.Method) {
		return api(405, "method_not_allowed", "代理不支持该请求方法")
	}
	cookieRequest := r.Clone(ctx)
	cookieRequest.Header = r.Header.Clone()
	cookieRequest.Header.Del("Authorization")
	cookieRequest.Header.Set("X-CSRF-Token", r.Header.Get("X-JMeter-CSRF-Token"))
	if _, e := p.authenticate(cookieRequest, true); e != nil {
		return e
	}
	projects := r.Header.Values("X-JMeter-Project-ID")
	if len(projects) != 1 || validID(projects[0]) != nil {
		return api(400, "invalid_project", "须提供唯一有效的X-JMeter-Project-ID项目请求头")
	}
	project, e := p.projectGetQ(contextQueryer{ctx, p.DB}, projects[0], false)
	if e != nil {
		return e
	}
	if project["archived"] == true {
		return api(409, "project_archived", "归档项目不能调试接口")
	}
	// This hop authenticates only with the platform cookie and namespaced CSRF.
	// An accidentally reused platform bearer must never become target credentials.
	if p.Config.AdminToken != "" {
		for _, value := range r.Header.Values("Authorization") {
			// Intermediaries may combine duplicate fields. Inspect every bearer
			// credential without rewriting unrelated target authorization values.
			for _, part := range strings.Split(value, ",") {
				authorization := strings.Fields(part)
				if len(authorization) >= 2 && strings.EqualFold(authorization[0], "Bearer") && containsPlatformCredential(strings.Join(authorization[1:], " "), p.Config.AdminToken) {
					return api(400, "platform_authorization_forbidden", "平台认证令牌不能作为目标接口的业务认证")
				}
			}
		}
	}
	if e := validateHTTPInterfaceProtocol(r); e != nil {
		return e
	}
	values := r.URL.Query()["scalar_url"]
	if len(values) != 1 || len(values[0]) > 16384 {
		return api(400, "invalid_proxy_target", "须提供唯一scalar_url代理目标")
	}
	target, e := proxyTarget(values[0])
	if e != nil {
		return e
	}
	if r.ContentLength > proxyRequestLimit {
		return api(413, "proxy_request_too_large", "代理请求体超过6MiB")
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, proxyRequestLimit+1))
	if e != nil {
		return api(400, "invalid_proxy_body", "无法读取代理请求体")
	}
	if len(body) > proxyRequestLimit {
		return api(413, "proxy_request_too_large", "代理请求体超过6MiB")
	}
	request, e := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if e != nil {
		return api(400, "invalid_proxy_target", "代理目标地址无效")
	}
	request.Header = proxyRequestHeaders(r.Header)
	response, raw, e := p.proxyRoundTrip(ctx, request)
	if e != nil {
		return e
	}
	headers := response.Header.Clone()
	stripHopHeaders(headers)
	cookies := headers.Values("Set-Cookie")
	for key, values := range headers {
		lower := strings.ToLower(key)
		if lower == "set-cookie" || lower == "content-length" || lower == "location" || lower == "access-control-allow-origin" || lower == "access-control-allow-credentials" || lower == "clear-site-data" || lower == "x-scalar-set-cookie" {
			continue
		}
		w.Header()[key] = append([]string(nil), values...)
	}
	for _, cookie := range cookies {
		w.Header().Add("X-Scalar-Set-Cookie", cookie)
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "HEAD" && response.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	}
	w.WriteHeader(response.StatusCode)
	if r.Method != "HEAD" {
		_, e = w.Write(raw)
	}
	return e
}

// Match the known platform credential even when a malformed bearer value wraps
// it in quotes or appends parameters. A distinct token with a common prefix
// remains a business credential and is forwarded unchanged.
func containsPlatformCredential(value, token string) bool {
	if token == "" {
		return false
	}
	part := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~+/", c) >= 0
	}
	for offset := 0; offset < len(value); {
		index := strings.Index(value[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(token)
		if (index == 0 || !part(value[index-1])) && (end == len(value) || !part(value[end])) {
			return true
		}
		offset = index + 1
	}
	return false
}

// One bounded, DNS-pinned transport serves both Scalar proxying and collection
// debug. It never follows redirects and never consults HTTP_PROXY.
func (p *Platform) proxyRoundTrip(ctx context.Context, request *http.Request) (*http.Response, []byte, error) {
	stripCallerContextHeaders(request.Header)
	if e := validateHTTPInterfaceProtocol(request); e != nil {
		return nil, nil, e
	}
	target := request.URL
	ips, e := resolveProxyIPs(ctx, p.Config, target.Hostname())
	if e != nil {
		return nil, nil, e
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	dial := dialer.DialContext
	if p.Config.proxyTestDial != nil {
		dial = p.Config.proxyTestDial
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, ip := range ips {
			conn, e := dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second, MaxResponseHeaderBytes: 64 * 1024, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, api(504, "proxy_timeout", "代理请求超时")
		}
		return nil, nil, api(502, "proxy_failed", "代理目标连接失败或响应中断")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != 304 {
		return nil, nil, api(502, "proxy_redirect_forbidden", "代理不跟随目标重定向，请直接使用最终地址")
	}
	if request.Method != "HEAD" && response.ContentLength > proxyResponseLimit {
		return nil, nil, api(502, "proxy_response_too_large", "代理目标响应超过4MiB")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, proxyResponseLimit+1))
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, api(504, "proxy_timeout", "代理请求超时")
		}
		return nil, nil, api(502, "proxy_failed", "读取代理目标响应失败")
	}
	if len(raw) > proxyResponseLimit {
		return nil, nil, api(502, "proxy_response_too_large", "代理目标响应超过4MiB")
	}

	return response, raw, nil
}
