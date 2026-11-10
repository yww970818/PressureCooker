// Package interfaceprotocol owns the protocol/serialization capability boundary.
// Reserved descriptors are persisted as metadata, never interpreted as HTTP.
package interfaceprotocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Status string

const (
	Supported Status = "supported"
	Reserved  Status = "reserved"
)

type Protocol struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         Status   `json:"status"`
	Transports     []string `json:"transports"`
	Serializations []string `json:"serializations"`
}
type Serialization struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status Status `json:"status"`
}
type RPC struct {
	Service   string `json:"service,omitempty"`
	Method    string `json:"method,omitempty"`
	Streaming string `json:"streaming,omitempty"`
}
type Protobuf struct {
	SchemaRef     string `json:"schema_ref,omitempty"`
	DescriptorRef string `json:"descriptor_ref,omitempty"`
	MessageType   string `json:"message_type,omitempty"`
}
type Descriptor struct {
	Protocol      string    `json:"protocol"`
	Serialization string    `json:"serialization"`
	RPC           *RPC      `json:"rpc,omitempty"`
	Protobuf      *Protobuf `json:"protobuf,omitempty"`
}

func Protocols() []Protocol {
	return []Protocol{
		{"http", "HTTP", Supported, []string{"http", "https"}, []string{"json", "text", "binary", "protobuf"}},
		{"websocket", "WebSocket", Supported, []string{"ws", "wss"}, []string{"json", "text", "binary", "protobuf"}},
		{"rpc", "通用 RPC", Reserved, []string{}, []string{"json", "text", "binary", "protobuf"}},
		{"grpc", "gRPC", Reserved, []string{"http2"}, []string{"protobuf", "json"}},
	}
}
func Serializations() []Serialization {
	return []Serialization{{"json", "JSON", Supported}, {"text", "文本", Supported}, {"binary", "二进制", Reserved}, {"protobuf", "Protocol Buffers", Reserved}}
}
func Normalize(d Descriptor) (Descriptor, error) {
	var protocol *Protocol
	for _, p := range Protocols() {
		if p.ID == d.Protocol {
			value := p
			protocol = &value
			break
		}
	}
	if protocol == nil {
		return Descriptor{}, fmt.Errorf("unknown protocol")
	}
	serializationKnown := false
	for _, s := range protocol.Serializations {
		if s == d.Serialization {
			serializationKnown = true
		}
	}
	if !serializationKnown {
		return Descriptor{}, fmt.Errorf("unknown or incompatible serialization")
	}
	if d.RPC != nil {
		if d.Protocol != "rpc" && d.Protocol != "grpc" {
			return Descriptor{}, fmt.Errorf("RPC metadata requires RPC protocol")
		}
		switch d.RPC.Streaming {
		case "", "unary", "client", "server", "bidirectional":
		default:
			return Descriptor{}, fmt.Errorf("invalid RPC streaming mode")
		}
		for _, text := range []string{d.RPC.Service, d.RPC.Method} {
			if !validText(text) {
				return Descriptor{}, fmt.Errorf("invalid RPC descriptor")
			}
		}
	}
	if d.Protobuf != nil && d.Serialization != "protobuf" {
		return Descriptor{}, fmt.Errorf("Protobuf metadata requires protobuf serialization")
	}
	if d.Protobuf != nil {
		for _, text := range []string{d.Protobuf.SchemaRef, d.Protobuf.DescriptorRef, d.Protobuf.MessageType} {
			if !validText(text) {
				return Descriptor{}, fmt.Errorf("invalid Protobuf descriptor")
			}
		}
	}
	return d, nil
}

func validText(text string) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= 512 && !strings.ContainsAny(text, "\x00\r\n")
}

// Parse accepts only the registered descriptor shape, with no remote lookup or
// schema loading. Empty optional objects are useful while authoring a request.
func Parse(raw any) (Descriptor, error) {
	data, ok := raw.(map[string]any)
	if !ok {
		return Descriptor{}, fmt.Errorf("descriptor must be an object")
	}
	for _, key := range []string{"protocol", "serialization"} {
		if _, ok := data[key].(string); !ok {
			return Descriptor{}, fmt.Errorf("descriptor IDs must be strings")
		}
	}
	for _, key := range []string{"rpc", "protobuf"} {
		value, exists := data[key]
		if !exists {
			continue
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return Descriptor{}, fmt.Errorf("nested descriptor must be an object")
		}
		for _, value := range fields {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" || !validText(text) {
				return Descriptor{}, fmt.Errorf("descriptor fields must be bounded strings")
			}
		}
	}
	encoded, e := json.Marshal(data)
	if e != nil || len(encoded) > 8192 {
		return Descriptor{}, fmt.Errorf("descriptor too large")
	}
	var descriptor Descriptor
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&descriptor); e != nil {
		return Descriptor{}, fmt.Errorf("invalid descriptor fields")
	}
	return Normalize(descriptor)
}
func ExecutionStatus(d Descriptor) Status {
	if _, e := Normalize(d); e != nil {
		return Reserved
	}
	for _, p := range Protocols() {
		if p.ID == d.Protocol && p.Status == Reserved {
			return Reserved
		}
	}
	for _, s := range Serializations() {
		if s.ID == d.Serialization && s.Status == Reserved {
			return Reserved
		}
	}
	return Supported
}
