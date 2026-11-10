package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

func constant(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }
func sessionHash(key, sid string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(sid))
	return hex.EncodeToString(h.Sum(nil))
}
func token() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func (p *Platform) login(value any) (string, Obj, error) {
	v, ok := value.(string)
	if !ok || !constant(v, p.Config.AdminToken) {
		return "", nil, api(401, "unauthorized", "Invalid credentials")
	}
	sid, csrf := token(), token()
	e := p.transaction(func(tx *Tx) error {
		if _, e := tx.Exec("DELETE FROM sessions WHERE expires<?", epoch()); e != nil {
			return e
		}
		_, e := tx.Exec("INSERT INTO sessions VALUES(?,?,?)", sessionHash(p.Config.SessionKey, sid), csrf, epoch()+float64(p.Config.SessionSeconds))
		return e
	})
	if e != nil {
		return "", nil, e
	}
	if e = p.audit("session.login", nil); e != nil {
		return "", nil, e
	}
	return sid, Obj{"authenticated": true, "csrf_token": csrf}, nil
}
func (p *Platform) authenticate(r *http.Request, write bool) (Obj, error) {
	authorization := r.Header.Get("Authorization")
	if authorization != "" {
		if !strings.HasPrefix(authorization, "Bearer ") || !constant(strings.TrimPrefix(authorization, "Bearer "), p.Config.AdminToken) {
			return nil, api(401, "unauthorized", "Authentication required")
		}
		return Obj{"authenticated": true, "csrf_token": nil, "bearer": true}, nil
	}
	sid := ""
	if c, e := r.Cookie("jmeter_session"); e == nil {
		sid = c.Value
	}
	hashed := sessionHash(p.Config.SessionKey, sid)
	var csrf string
	var expires float64
	e := p.DB.QueryRowContext(r.Context(), "SELECT csrf,expires FROM sessions WHERE id=?", hashed).Scan(&csrf, &expires)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(401, "unauthorized", "Authentication required")
	}
	if e != nil {
		return nil, e
	}
	if expires <= epoch() {
		return nil, api(401, "unauthorized", "Authentication required")
	}
	if write && !constant(r.Header.Get("X-CSRF-Token"), csrf) {
		return nil, api(403, "csrf_required", "Valid X-CSRF-Token required")
	}
	return Obj{"authenticated": true, "csrf_token": csrf, "session_id": hashed}, nil
}
func (p *Platform) logout(auth Obj) error {
	if id := str(auth["session_id"]); id != "" {
		if _, e := p.DB.Exec("DELETE FROM sessions WHERE id=?", id); e != nil {
			return e
		}
	}
	return p.audit("session.logout", nil)
}
