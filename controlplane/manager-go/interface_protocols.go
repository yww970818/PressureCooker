package main

import (
	"net/http"
	"strings"

	"pressurecooker/manager/internal/interfaceprotocol"
)

const interfaceProtocolExtension = "x-pressure-protocol"

func interfaceProtocolCatalog() Obj {
	return Obj{"protocols": interfaceprotocol.Protocols(), "serializations": interfaceprotocol.Serializations(), "metadata_extension": interfaceProtocolExtension}
}
func decodeInterfaceProtocol(raw any) (interfaceprotocol.Descriptor, error) {
	normalized, e := interfaceprotocol.Parse(raw)
	if e != nil {
		return interfaceprotocol.Descriptor{}, api(400, "invalid_interface_protocol", "协议元数据无效；须提供已登记protocol、serialization及受支持的RPC/Protobuf描述字段")
	}
	return normalized, nil
}
func operationInterfaceProtocol(document, operation Obj) (interfaceprotocol.Descriptor, error) {
	raw, explicit := operation[interfaceProtocolExtension]
	// A legacy per-operation WS configuration is more specific than a document
	// default. This lets mixed HTTP/WS collections adopt metadata incrementally.
	if !explicit && operation["x-pressure-ws"] != nil {
		return interfaceprotocol.Descriptor{Protocol: "websocket", Serialization: "json"}, nil
	}
	if !explicit {
		raw, explicit = document[interfaceProtocolExtension]
	}
	descriptor := interfaceprotocol.Descriptor{Protocol: "http", Serialization: "json"}
	if !explicit {
		for media := range obj(obj(operation["requestBody"])["content"]) {
			media = strings.ToLower(media)
			if strings.HasPrefix(media, "application/grpc") {
				descriptor.Protocol, descriptor.Serialization = "grpc", "protobuf"
				break
			}
			if strings.Contains(media, "protobuf") {
				descriptor.Serialization = "protobuf"
			}
		}
	}
	var e error
	if explicit {
		descriptor, e = decodeInterfaceProtocol(raw)
		if e != nil {
			return descriptor, e
		}
	}
	if operation["x-pressure-ws"] != nil {
		// Old collections need no new metadata. Conflicting explicit metadata is
		// rejected so the same operation can never acquire two execution meanings.
		if explicit && descriptor.Protocol != "websocket" {
			return descriptor, api(400, "invalid_interface_protocol", "x-pressure-ws与显式协议元数据冲突")
		}
		descriptor.Protocol = "websocket"
	}
	return descriptor, nil
}
func requireInterfaceProtocolExecution(descriptor interfaceprotocol.Descriptor) error {
	if interfaceprotocol.ExecutionStatus(descriptor) == interfaceprotocol.Reserved {
		return api(400, "interface_protocol_reserved", "RPC、gRPC、Protobuf及二进制消息目前仅保留描述扩展，尚未接入调试或工作流执行")
	}
	return nil
}

// The generic Scalar proxy has no collection context. Recognized transport
// hints and typed content headers still cannot silently enter its HTTP path.
func validateHTTPInterfaceProtocol(r *http.Request) error {
	protocol, serialization := r.Header.Get("X-Pressure-Protocol"), r.Header.Get("X-Pressure-Serialization")
	if protocol == "" {
		protocol = "http"
	}
	if serialization == "" {
		serialization = "json"
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if strings.HasPrefix(contentType, "application/grpc") {
		protocol = "grpc"
	}
	if strings.Contains(contentType, "protobuf") {
		serialization = "protobuf"
	}
	descriptor, e := interfaceprotocol.Normalize(interfaceprotocol.Descriptor{Protocol: protocol, Serialization: serialization})
	if e != nil {
		return api(400, "invalid_interface_protocol", "HTTP代理协议或序列化类型无效")
	}
	if e := requireInterfaceProtocolExecution(descriptor); e != nil {
		return e
	}
	if descriptor.Protocol != "http" {
		return api(400, "invalid_interface_protocol", "当前代理仅接受HTTP协议")
	}
	return nil
}
func validateCollectionInterfaceProtocols(document Obj) error {
	if raw, exists := document[interfaceProtocolExtension]; exists {
		if _, e := decodeInterfaceProtocol(raw); e != nil {
			return e
		}
	}
	for _, path := range objectKeys(obj(document["paths"])) {
		entry := obj(obj(document["paths"])[path])
		for method, raw := range entry {
			if !apiMethod(method) {
				continue
			}
			if _, e := operationInterfaceProtocol(document, obj(raw)); e != nil {
				return e
			}
		}
	}
	return nil
}
