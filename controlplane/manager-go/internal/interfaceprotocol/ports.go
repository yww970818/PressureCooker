package interfaceprotocol

import (
	"context"
	"encoding/json"
	"fmt"
)

// Ports separates descriptor validation, online debugging, JMX compilation and
// target runtime evidence. An editor definition never registers execution ports.
// Implementations must be explicitly wired by the host; RPC descriptors alone
// cannot acquire an HTTP fallback.
type Ports interface {
	Validate(context.Context, Descriptor, json.RawMessage) error
	Debug(context.Context, Descriptor, json.RawMessage) (json.RawMessage, error)
	Compile(context.Context, Descriptor, json.RawMessage) ([]byte, error)
	Capabilities(context.Context) ([]Capability, error)
}

type Capability struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	NodeID     string `json:"node_id,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// Registry is configured at startup, before concurrent request handling. A
// provider is selected by exact protocol/serialization, never by request errors.
type Registry struct{ providers map[string]Ports }

func NewRegistry() *Registry { return &Registry{providers: map[string]Ports{}} }
func (r *Registry) Register(d Descriptor, ports Ports) error {
	if _, err := Normalize(d); err != nil {
		return err
	}
	if ports == nil {
		return fmt.Errorf("protocol ports required")
	}
	key := d.Protocol + "/" + d.Serialization
	if _, exists := r.providers[key]; exists {
		return fmt.Errorf("protocol ports already registered")
	}
	r.providers[key] = ports
	return nil
}
func (r *Registry) Resolve(d Descriptor) (Ports, bool) {
	if _, err := Normalize(d); err != nil {
		return nil, false
	}
	ports, ok := r.providers[d.Protocol+"/"+d.Serialization]
	return ports, ok
}
