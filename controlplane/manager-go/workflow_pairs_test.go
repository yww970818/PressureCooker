package main

import (
	"strings"
	"testing"
)

func TestWorkflowPairEditsRetainTheirOwnUnknownProperties(t *testing.T) {
	for _, test := range []struct{ component, collection, itemType, prefix, key string }{{"Arguments", "Arguments.arguments", "Argument", "Argument", "variables"}, {"HeaderManager", "HeaderManager.headers", "Header", "Header", "headers"}} {
		for _, mode := range []string{"delete-first-and-rename", "reorder", "insert-new", "duplicate-names"} {
			t.Run(test.component+"/"+mode, func(t *testing.T) {
				parent := element(test.component)
				collection := ensureCollection(parent, test.collection)
				for _, name := range []string{"A", "B"} {
					entry := element("elementProp", "name", name, "elementType", test.itemType, "custom", name)
					entry.setProp("stringProp", test.prefix+".name", name)
					entry.setProp("stringProp", test.prefix+".value", "value-"+name)
					entry.setProp("stringProp", "plugin.setting", "extra-"+name)
					collection.append(entry)
				}
				rawBefore := elementXML(parent)
				pairs := readPairs(parent, test.collection, test.itemType, test.prefix)
				switch mode {
				case "delete-first-and-rename":
					pairs = pairs[1:]
					obj(pairs[0])["name"] = "renamed-B"
				case "reorder":
					pairs = []any{pairs[1], pairs[0]}
				case "insert-new":
					pairs = []any{Obj{"name": "new", "value": "new-value"}, pairs[1], pairs[0]}
				case "duplicate-names":
					obj(pairs[0])["name"] = "duplicate"
					obj(pairs[1])["name"] = "duplicate"
					pairs = []any{pairs[1], pairs[0]}
				}
				copy := parent.copy()
				patchPairs(copy, test.collection, test.itemType, test.prefix, pairs)
				entries := copy.prop(test.collection).children()
				for i, value := range pairs {
					pair := obj(value)
					expected := strings.TrimPrefix(str(pair["value"]), "value-")
					if expected == "new-value" {
						if entries[i].attr("custom") != "" || entries[i].prop("plugin.setting") != nil {
							t.Fatal("new pair inherited another row's unsupported properties")
						}
						continue
					}
					if entries[i].attr("custom") != expected || entries[i].propText("plugin.setting") != "extra-"+expected {
						t.Fatalf("surviving %s row inherited another source: %s", expected, elementXML(entries[i]))
					}
					if entries[i].propText(test.prefix+".name") != str(pair["name"]) {
						t.Fatal("rename not applied")
					}
				}
				if elementXML(parent) != rawBefore {
					t.Fatal("source XML mutated by editing a clone")
				}
				noChange := parent.copy()
				patchPairs(noChange, test.collection, test.itemType, test.prefix, readPairs(parent, test.collection, test.itemType, test.prefix))
				if elementXML(noChange) != rawBefore {
					t.Fatal("non-mutating roundtrip changed source XML")
				}
			})
		}
	}
}
