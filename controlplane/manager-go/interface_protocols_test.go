package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestInterfaceProtocolRegistryNormalizationAndExecutionBoundary(t *testing.T) {
	legacy, e := operationInterfaceProtocol(Obj{}, Obj{"x-pressure-ws": Obj{"url": "wss://example.invalid"}})
	if e != nil || legacy.Protocol != "websocket" {
		t.Fatal(legacy, e)
	}
	legacy, e = operationInterfaceProtocol(Obj{"x-pressure-protocol": Obj{"protocol": "http", "serialization": "json"}}, Obj{"x-pressure-ws": Obj{"url": "wss://example.invalid"}})
	if e != nil || legacy.Protocol != "websocket" {
		t.Fatal("document default overrode legacy operation transport", legacy, e)
	}
	grpc := Obj{"x-pressure-protocol": Obj{"protocol": "grpc", "serialization": "protobuf", "rpc": Obj{"service": "example.Greeter", "method": "SayHello", "streaming": "unary"}, "protobuf": Obj{"descriptor_ref": "descriptor-local", "message_type": "example.Request"}}}
	d, e := operationInterfaceProtocol(Obj{}, grpc)
	if e != nil || d.Protocol != "grpc" || d.RPC.Service != "example.Greeter" {
		t.Fatal(d, e)
	}
	if e := requireInterfaceProtocolExecution(d); e == nil {
		t.Fatal("reserved grpc entered execution")
	}
	for _, media := range []string{"application/grpc+proto", "application/vnd.google.protobuf"} {
		inferred, e := operationInterfaceProtocol(Obj{}, Obj{"requestBody": Obj{"content": Obj{media: Obj{}}}})
		if e != nil || requireInterfaceProtocolExecution(inferred) == nil {
			t.Fatal("typed imported operation fell back to HTTP", media, inferred, e)
		}
	}
	if e := validateCollectionInterfaceProtocols(Obj{"paths": Obj{"/rpc": Obj{"post": grpc}}}); e != nil {
		t.Fatal("reserved metadata cannot be stored", e)
	}
	if _, e := exportPostman(Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.invalid"}}, "paths": Obj{"/rpc": Obj{"post": grpc}}}}); e == nil {
		t.Fatal("grpc exported as HTTP")
	}
	for _, raw := range []any{"grpc", Obj{"protocol": "unknown", "serialization": "json"}, Obj{"protocol": "http", "serialization": "json", "url": "https://example.invalid"}, Obj{"protocol": "http", "serialization": "json", "rpc": Obj{"method": "X"}}, Obj{"protocol": "grpc", "serialization": "protobuf", "rpc": Obj{"streaming": "unsupported"}}} {
		if _, e := operationInterfaceProtocol(Obj{}, Obj{"x-pressure-protocol": raw}); e == nil {
			t.Fatal("invalid metadata accepted", raw)
		}
	}
	if _, e := componentConfig("HTTPSamplerProxy", Obj{"protocol": "grpc"}); e == nil {
		t.Fatal("grpc compiled as HTTP sampler")
	}
	paths := obj(openAPI()["paths"])
	if obj(paths["/api/interface-protocols"])["get"] == nil {
		t.Fatal("protocol registry not discovered")
	}
}

func TestScalarProxyReservedMetadataNeverReachesTarget(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	p := proxyPlatform(t, target)
	for _, contentType := range []string{"application/grpc", "application/grpc+proto", "application/x-protobuf"} {
		r := makeProxyRequest(target, "POST", nil)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("reserved HTTP fallback returned %d: %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("reserved request reached network")
	}
}

func TestHTTPProxyRejectsReservedProtocolAndSerializationBeforeTransport(t *testing.T) {
	for _, header := range []map[string]string{
		{"X-Pressure-Protocol": "grpc"},
		{"X-Pressure-Protocol": "rpc"},
		{"X-Pressure-Serialization": "protobuf"},
		{"Content-Type": "application/grpc+proto"},
		{"Content-Type": "application/x-protobuf"},
	} {
		r := httptest.NewRequest("POST", "/api/api-proxy?scalar_url=https://example.invalid", nil)
		for key, value := range header {
			r.Header.Set(key, value)
		}
		if e := validateHTTPInterfaceProtocol(r); e == nil {
			t.Fatal("reserved transport request accepted", header)
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/json")
	if e := validateHTTPInterfaceProtocol(r); e != nil {
		t.Fatal(e)
	}
}
