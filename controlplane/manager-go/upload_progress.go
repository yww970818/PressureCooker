package main

import (
	"io"
	"net/http"
	"time"
)

type requestProgressBody struct {
	io.ReadCloser
	control *http.ResponseController
	idle    time.Duration
}

func (b *requestProgressBody) Read(data []byte) (int, error) {
	_ = b.control.SetReadDeadline(time.Now().Add(b.idle))
	n, e := b.ReadCloser.Read(data)
	if n > 0 {
		_ = b.control.SetReadDeadline(time.Now().Add(b.idle))
	}
	return n, e
}
func progressRequestBody(w http.ResponseWriter, r *http.Request, idle time.Duration) func() {
	if idle <= 0 {
		idle = 30 * time.Second
	}
	control := http.NewResponseController(w)
	r.Body = &requestProgressBody{ReadCloser: r.Body, control: control, idle: idle}
	return func() { _ = control.SetReadDeadline(time.Now().Add(idle)) }
}
