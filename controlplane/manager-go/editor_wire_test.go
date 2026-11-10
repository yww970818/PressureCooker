package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEditorWirePoolsRepeatedTextWithoutMutatingOriginal(t *testing.T) {
	text := strings.Repeat("原文\n{{cookie}}\"😀", 400)
	input := Obj{"graph": Obj{"nodes": []any{Obj{"id": "a", "properties": Obj{"config": Obj{"body": text, "literal": Obj{"text_ref": 0}}}}, Obj{"id": "b", "properties": Obj{"config": Obj{"body": text, "nil": nil}}}}}}
	before := string(encoded(input))
	wire := editorReadPayload(input)
	if wire["format"] != "editor-text-v1" || len(arr(wire["texts"])) != 1 || len(arr(wire["slots"])) != 2 {
		t.Fatal("text pool did not remove repeated text")
	}
	if string(encoded(input)) != before {
		t.Fatal("wire encoding changed authored graph")
	}
	if len(encoded(wire)) >= len(before)*3/4 {
		t.Fatal("pooled response did not reduce duplicate source")
	}
	if obj(obj(arr(obj(wire["data"])["graph"].(Obj)["nodes"])[0])["properties"])["config"] == nil {
		t.Fatal("lost node configuration")
	}
}

func TestEditorWireKeepsDeepJSONBounded(t *testing.T) {
	value := Obj{"text": strings.Repeat("text", 1000)}
	for i := 0; i < 80; i++ {
		value = Obj{"nested": value}
	}
	wire := editorReadPayload(value)
	if wire["format"] != nil || string(encoded(wire)) != string(encoded(value)) {
		t.Fatal("deep JSON should remain intact without an expanding path table")
	}
}

func TestEditorReadCachedJSONIntegrity(t *testing.T) {
	for _, value := range []any{Obj{"message": "中文 😀 {{value}}", "null": nil}, Obj{"numbers": []any{1, 1.5, true, false}}, Obj{}} {
		packed, e := packReadModel(value)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := unpackEditorRead(packed)
		if e != nil {
			t.Fatal(e)
		}
		restored, e := decode(raw)
		if e != nil || string(encoded(restored)) != string(encoded(value)) {
			t.Fatal("cached JSON changed original data")
		}
	}
	for _, value := range []any{[]any{1, 2}, "literal", nil} {
		packed, _ := packReadModel(value)
		if _, e := unpackEditorRead(packed); e == nil {
			t.Fatal("non-object cache accepted")
		}
	}
	packed, _ := packReadModel(Obj{"kept": true})
	compressed, _ := base64.RawStdEncoding.DecodeString(packed[6:])
	compressed[len(compressed)-8] ^= 0xff
	broken := "gzip1:" + base64.RawStdEncoding.EncodeToString(compressed)
	if _, e := unpackEditorRead(broken); e == nil {
		t.Fatal("corrupt gzip cache accepted")
	}
	if _, e := unpackEditorRead("future:abc"); e == nil {
		t.Fatal("unknown cache format accepted")
	}
}
