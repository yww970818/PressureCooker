package interfaceprotocol

import (
	"context"
	"encoding/json"
	"testing"
)

type testPorts struct{}

func (testPorts) Validate(context.Context, Descriptor, json.RawMessage) error { return nil }
func (testPorts) Debug(context.Context, Descriptor, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}
func (testPorts) Compile(context.Context, Descriptor, json.RawMessage) ([]byte, error) {
	return nil, nil
}
func (testPorts) Capabilities(context.Context) ([]Capability, error) { return nil, nil }
func TestExecutionPortsRequireExactExplicitRegistration(t *testing.T) {
	r := NewRegistry()
	d := Descriptor{Protocol: "grpc", Serialization: "protobuf"}
	if _, ok := r.Resolve(d); ok {
		t.Fatal("reserved descriptor registered itself")
	}
	if err := r.Register(d, testPorts{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Resolve(d); !ok {
		t.Fatal("explicit port missing")
	}
	if _, ok := r.Resolve(Descriptor{Protocol: "http", Serialization: "protobuf"}); ok {
		t.Fatal("protocol fallback")
	}
	if err := r.Register(d, testPorts{}); err == nil {
		t.Fatal("duplicate provider")
	}
	if err := r.Register(Descriptor{Protocol: "other", Serialization: "json"}, testPorts{}); err == nil {
		t.Fatal("unknown descriptor")
	}
	if err := r.Register(Descriptor{Protocol: "http", Serialization: "json"}, nil); err == nil {
		t.Fatal("nil provider")
	}
}
