package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// This synthetic fixture keeps only aggregate counters and unused transport IDs.
// Payloads are never logged or retained after a socket closes.
type counters struct {
	Active             int64 `json:"active"`
	Opened             int64 `json:"opened"`
	Closed             int64 `json:"closed"`
	SocketIOHandshakes int64 `json:"socketio_handshakes"`
	HeartbeatEchoes    int64 `json:"heartbeat_echoes"`
}

type fixture struct {
	mu        sync.Mutex
	stats     counters
	pending   map[string]time.Time
	nextToken int64
	nextConn  int64
	upgrader  websocket.Upgrader
}

func newFixture() http.Handler {
	f := &fixture{
		pending: make(map[string]time.Time),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if requireGET(w, r) {
			writeHTTPJSON(w, map[string]any{"status": "ok"})
		}
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if !requireGET(w, r) {
			return
		}
		f.mu.Lock()
		stats := f.stats
		f.mu.Unlock()
		writeHTTPJSON(w, stats)
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		if requireGET(w, r) {
			writeHTTPJSON(w, map[string]any{"code": 200, "value": r.URL.Query().Get("value")})
		}
	})
	mux.HandleFunc("/ws", f.handleEcho)
	mux.HandleFunc("/socket.io/1/", f.handleSocketIO)
	return mux
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "GET required", http.StatusMethodNotAllowed)
	return false
}

func writeHTTPJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(value)
}

// Gorilla permits one concurrent writer; all application writes use this lock.
type socket struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	owner   string
	f       *fixture
}

func (f *fixture) upgrade(w http.ResponseWriter, r *http.Request) (*socket, error) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	f.mu.Lock()
	f.nextConn++
	owner := fmt.Sprintf("connection-%d", f.nextConn)
	f.stats.Active++
	f.stats.Opened++
	f.mu.Unlock()
	return &socket{conn: conn, owner: owner, f: f}, nil
}

func (s *socket) close() {
	s.conn.Close()
	s.f.mu.Lock()
	s.f.stats.Active--
	s.f.stats.Closed++
	s.f.mu.Unlock()
}

func (s *socket) write(kind int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return s.conn.WriteMessage(kind, data)
}

func (s *socket) json(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.write(websocket.TextMessage, data)
}

func (f *fixture) handleEcho(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	s, err := f.upgrade(w, r)
	if err != nil {
		return
	}
	defer s.close()
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		var input map[string]any
		if kind != websocket.TextMessage || json.Unmarshal(data, &input) != nil || input == nil {
			if s.write(kind, data) != nil {
				return
			}
			continue
		}
		if delay, ok := input["delay_ms"].(float64); ok && delay > 0 {
			if delay > 1000 {
				delay = 1000
			}
			time.Sleep(time.Duration(delay * float64(time.Millisecond)))
		}
		if input["command"] == "scenario" {
			if s.json(map[string]any{"event": "notice", "owner": s.owner}) != nil {
				return
			}
		}
		if s.json(map[string]any{"event": "reply", "value": input["value"], "owner": s.owner, "received": input}) != nil {
			return
		}
	}
}

func (f *fixture) handleSocketIO(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	if r.URL.Path == "/socket.io/1/" {
		now := time.Now()
		f.mu.Lock()
		for id, expires := range f.pending {
			if !expires.After(now) {
				delete(f.pending, id)
			}
		}
		f.nextToken++
		id := fmt.Sprintf("fresh-%d", f.nextToken)
		f.pending[id] = now.Add(60 * time.Second)
		f.stats.SocketIOHandshakes++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "%s:60:60:websocket", id)
		return
	}
	const prefix = "/socket.io/1/websocket/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	f.mu.Lock()
	expires, issued := f.pending[id]
	delete(f.pending, id) // Claim atomically, so concurrent upgrades cannot reuse it.
	f.mu.Unlock()
	if !issued || !expires.After(time.Now()) {
		http.Error(w, "fresh handshake ID required", http.StatusForbidden)
		return
	}
	s, err := f.upgrade(w, r)
	if err != nil {
		return
	}
	defer s.close()
	if s.write(websocket.TextMessage, []byte("1::")) != nil {
		return
	}
	done := make(chan struct{})
	defer close(done)
	go s.heartbeat(done)
	for {
		kind, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.TextMessage {
			switch string(data) {
			case "0::":
				return
			case "2::":
				f.mu.Lock()
				f.stats.HeartbeatEchoes++
				f.mu.Unlock()
				continue
			}
		}
		// Application frames are opaque. Only transport control frames above
		// have meaning here; preserve every other frame's type and bytes.
		if s.write(kind, data) != nil {
			return
		}
	}
}

func (s *socket) heartbeat(done <-chan struct{}) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if s.write(websocket.TextMessage, []byte("2::")) != nil {
				s.conn.Close()
				return
			}
		}
	}
}

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	server := &http.Server{
		Addr: ":" + port, Handler: newFixture(),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("workflow WS fixture listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
