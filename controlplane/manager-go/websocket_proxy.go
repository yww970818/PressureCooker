package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const wsFrameLimit = 256 * 1024

// A JSON string can expand each decoded byte to a six-byte escape sequence.
const wsControlLimit = 6*wsFrameLimit + 64*1024

var wsDebugSlots = make(chan struct{}, 32)
var socketIOID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type wsConnectSpec struct {
	Kind         string   `json:"kind"`
	CSRF         string   `json:"csrf_token"`
	ProjectID    string   `json:"project_id"`
	URL          string   `json:"url"`
	HandshakeURL string   `json:"handshake_url"`
	SocketIO     bool     `json:"socketio"`
	Protocols    []string `json:"protocols"`
	Headers      []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
}

func wsTarget(raw string) (*url.URL, error) {
	target, e := url.Parse(raw)
	if e != nil || (target.Scheme != "ws" && target.Scheme != "wss") {
		return nil, api(400, "invalid_ws_target", "WS 目标须为 ws:// 或 wss:// 地址")
	}
	check := *target
	if check.Scheme == "ws" {
		check.Scheme = "http"
	} else {
		check.Scheme = "https"
	}
	if _, e = proxyTarget(check.String()); e != nil {
		return nil, e
	}
	return target, nil
}
func wsHeaders(spec wsConnectSpec) (http.Header, error) {
	if len(spec.Headers) > 100 || len(spec.Protocols) > 16 {
		return nil, api(400, "invalid_ws_config", "请求头或子协议过多")
	}
	headers := http.Header{}
	for _, row := range spec.Headers {
		lower := strings.ToLower(row.Name)
		if !validAPIHeader(row.Name, row.Value) || len(row.Value) > 16384 || strings.HasPrefix(lower, "sec-websocket-") || strings.HasPrefix(lower, "x-jmeter-") || contains([]string{"host", "connection", "upgrade", "content-length", "transfer-encoding", "proxy-authorization", "proxy-connection"}, lower) {
			return nil, api(400, "invalid_ws_headers", "握手协议头由 WebSocket 自动生成；请移除 Host、Connection、Upgrade 和 Sec-WebSocket-* 等头")
		}
		headers.Add(row.Name, row.Value)
	}
	for _, protocol := range spec.Protocols {
		if len(protocol) > 200 || !regexp.MustCompile(`^[!#$%&'*+.^_`+"`"+`|~0-9A-Za-z-]+$`).MatchString(protocol) {
			return nil, api(400, "invalid_ws_protocol", "子协议格式无效")
		}
	}
	stripCallerContextHeaders(headers)
	return headers, nil
}
func (p *Platform) wsDial(ctx context.Context, target *url.URL, headers http.Header, protocols []string) (*websocket.Conn, error) {
	headers = headers.Clone()
	stripCallerContextHeaders(headers)
	ips, e := resolveProxyIPs(ctx, p.Config, target.Hostname())
	if e != nil {
		return nil, e
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "wss" {
			port = "443"
		}
	}
	dial := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if p.Config.proxyTestDial != nil {
		dial = p.Config.proxyTestDial
	}
	wsDialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: protocols, NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
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
	}}
	conn, response, e := wsDialer.DialContext(ctx, target.String(), headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if e != nil {
		return nil, api(502, "ws_connect_failed", "目标 WebSocket 建连失败，请检查地址、Origin、认证和网络")
	}
	conn.SetReadLimit(wsFrameLimit)
	return conn, nil
}
func (p *Platform) wsDestination(ctx context.Context, spec wsConnectSpec, headers http.Header) (*url.URL, string, error) {
	socketID := ""
	address := spec.URL
	if len(address) > 16384 {
		return nil, "", api(400, "invalid_ws_target", "WS 地址过长")
	}
	if spec.SocketIO {
		if strings.Count(address, "{socket_id}") != 1 {
			return nil, "", api(400, "invalid_socketio_target", "Socket.IO 地址须包含一个 {socket_id}")
		}
		handshake, e := proxyTarget(spec.HandshakeURL)
		if e != nil {
			return nil, "", e
		}
		candidate, e := wsTarget(strings.Replace(address, "{socket_id}", "pending", 1))
		if e != nil {
			return nil, "", e
		}
		if candidate.Host != handshake.Host || (candidate.Scheme == "wss") != (handshake.Scheme == "https") {
			return nil, "", api(400, "invalid_socketio_target", "Socket.IO 握手和 WS 须使用相同主机、端口和安全级别")
		}
		request, e := http.NewRequestWithContext(ctx, "GET", handshake.String(), nil)
		if e != nil {
			return nil, "", api(400, "invalid_socketio_target", "Socket.IO 握手地址无效")
		}
		request.Header = headers.Clone()
		response, raw, e := p.proxyRoundTrip(ctx, request)
		if e != nil {
			return nil, "", e
		}
		if response.StatusCode != 200 || len(raw) > 4096 {
			return nil, "", api(502, "socketio_handshake_failed", "Socket.IO 握手失败或响应过长")
		}
		fields := strings.Split(strings.TrimSpace(string(raw)), ":")
		if len(fields) != 4 || !socketIOID.MatchString(fields[0]) || !contains(strings.Split(fields[3], ","), "websocket") {
			return nil, "", api(502, "socketio_handshake_failed", "Socket.IO 1.x 握手响应无效")
		}
		socketID = fields[0]
		address = strings.Replace(address, "{socket_id}", socketID, 1)
	}
	target, err := wsTarget(address)
	return target, socketID, err
}
func (p *Platform) wsProxy(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "GET" {
		return api(405, "method_not_allowed", "WS 通道须使用 GET")
	}
	cookieRequest := r.Clone(r.Context())
	cookieRequest.Header = r.Header.Clone()
	cookieRequest.Header.Del("Authorization")
	if _, e := p.authenticate(cookieRequest, false); e != nil {
		return e
	}
	origin, e := url.Parse(r.Header.Get("Origin"))
	if e != nil || !contains([]string{"http", "https"}, origin.Scheme) || origin.Host != r.Host || origin.User != nil {
		return api(403, "ws_origin_forbidden", "WS 调试通道仅接受控制台同源连接")
	}
	select {
	case wsDebugSlots <- struct{}{}:
		defer func() { <-wsDebugSlots }()
	default:
		return api(429, "ws_debug_busy", "WS 调试连接已达上限（32）")
	}
	client, e := (&websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}).Upgrade(w, r, nil)
	if e != nil {
		return nil
	}
	defer client.Close()
	client.SetReadLimit(wsControlLimit)
	client.SetReadDeadline(time.Now().Add(10 * time.Second))
	var writeMu sync.Mutex
	send := func(event Obj) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		client.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return client.WriteJSON(event)
	}
	fail := func(e error) {
		_, code, message := requestFailure(e)
		if code == "csrf_required" || code == "unauthorized" {
			message = "会话校验失败，请刷新页面或重新登录"
		}
		if code == "internal_error" {
			message = "WS 转发服务异常，请稍后重试"
		}
		send(Obj{"kind": "error", "code": code, "message": message})
	}
	var spec wsConnectSpec
	if e = client.ReadJSON(&spec); e != nil {
		fail(api(400, "invalid_ws_config", "首帧须提供 WS 调试配置"))
		return nil
	}
	if spec.Kind != "connect" {
		fail(api(400, "invalid_ws_config", "首帧类型须为 connect"))
		return nil
	}
	cookieRequest.Header.Set("X-CSRF-Token", spec.CSRF)
	if _, e = p.authenticate(cookieRequest, true); e != nil {
		fail(e)
		return nil
	}
	if spec.ProjectID == "" || validID(spec.ProjectID) != nil {
		fail(api(400, "invalid_project", "须指定项目"))
		return nil
	}
	scoped := p.forProject(spec.ProjectID)
	project, e := scoped.projectGetQ(contextQueryer{r.Context(), p.DB}, spec.ProjectID, false)
	if e != nil {
		fail(e)
		return nil
	}
	if project["archived"] == true {
		fail(api(409, "project_archived", "归档项目不能调试接口"))
		return nil
	}
	headers, e := wsHeaders(spec)
	if e != nil {
		fail(e)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	target, socketID, e := p.wsDestination(ctx, spec, headers)
	if e != nil {
		cancel()
		fail(e)
		return nil
	}
	upstream, e := p.wsDial(ctx, target, headers, spec.Protocols)
	cancel()
	if e != nil {
		fail(e)
		return nil
	}
	defer upstream.Close()
	lifetime := time.AfterFunc(30*time.Minute, func() { upstream.Close(); client.Close() })
	defer lifetime.Stop()
	client.SetReadDeadline(time.Now().Add(30 * time.Minute))
	upstream.SetReadDeadline(time.Now().Add(30 * time.Minute))
	var upstreamMu sync.Mutex
	writeUpstream := func(data []byte) error {
		upstreamMu.Lock()
		defer upstreamMu.Unlock()
		upstream.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return upstream.WriteMessage(websocket.TextMessage, data)
	}
	outgoing := func(data []byte) error {
		if e := writeUpstream(data); e != nil {
			return e
		}
		return send(Obj{"kind": "frame", "direction": "sent", "data": string(data), "timestamp_ms": time.Now().UnixMilli()})
	}
	opened := Obj{"kind": "state", "state": "open"}
	if socketID != "" {
		opened["socket_id"] = socketID
	}
	if e = send(opened); e != nil {
		return nil
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer upstream.Close()
		for {
			var message struct {
				Kind string `json:"kind"`
				Data string `json:"data"`
			}
			if e := client.ReadJSON(&message); e != nil {
				return
			}
			if message.Kind == "close" {
				return
			}
			if message.Kind != "send" || len(message.Data) > wsFrameLimit {
				fail(api(400, "invalid_ws_message", "只支持不超过 256 KiB 的 send 消息"))
				return
			}
			if e := outgoing([]byte(message.Data)); e != nil {
				return
			}
		}
	}()
	for {
		kind, data, e := upstream.ReadMessage()
		if e != nil {
			select {
			case <-finished:
			default:
				if !websocket.IsCloseError(e, websocket.CloseNormalClosure, websocket.CloseGoingAway) && !errors.Is(e, net.ErrClosed) {
					fail(api(502, "ws_closed", "目标连接已断开或超过调试时限"))
				}
			}
			break
		}
		binary := kind == websocket.BinaryMessage
		text := string(data)
		if binary {
			text = base64.StdEncoding.EncodeToString(data)
		}
		if e = send(Obj{"kind": "frame", "direction": "received", "data": text, "binary": binary, "timestamp_ms": time.Now().UnixMilli()}); e != nil {
			break
		}
		if spec.SocketIO && kind == websocket.TextMessage && string(data) == "2::" {
			if e = outgoing([]byte("2::")); e != nil {
				break
			}
		}
	}
	send(Obj{"kind": "state", "state": "closed"})
	client.Close()
	<-finished
	return nil
}
