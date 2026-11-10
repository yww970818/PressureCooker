package workflowcatalog

import (
	_ "embed"
	"encoding/json"
)

// NativeJSON is derived from the pinned official distribution's save aliases
// and concrete TestElement classes, never from a class-name prefix guess.
//
//go:embed native.json
var NativeJSON []byte

type Component struct {
	TemplateProperties string `json:"template_properties"`
	Component          string `json:"component"`
	ClassName          string `json:"class_name"`
	Title              string `json:"title"`
	Kind               string `json:"kind"`
	GUI                string `json:"gui"`
	Source             string `json:"source"`
	Version            string `json:"version"`
}

var Native []Component
var ByName = map[string]Component{}

func init() {
	if err := json.Unmarshal(NativeJSON, &Native); err != nil {
		panic(err)
	}
	for _, c := range Native {
		ByName[c.Component] = c
		ByName[c.ClassName] = c
	}
}
