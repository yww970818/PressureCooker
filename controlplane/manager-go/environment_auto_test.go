package main

import (
	"testing"
	"time"
)

func TestEnvironmentAutomaticRulesValidateExactDeclarations(t *testing.T) {
	for _, tc := range []struct{ rule, kind string }{{"{{$auto.client_ip}}", "client_ip"}, {"{{$auto.timestamp_ms}}", "timestamp_ms"}} {
		kind, length, err := parseEnvironmentRandomRule(tc.rule)
		if err != nil || kind != tc.kind || length != 0 {
			t.Fatalf("valid auto rule rejected: %s %s %d %v", tc.rule, kind, length, err)
		}
		body := Obj{"name": "automatic", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "arbitrary_name", "value": tc.rule}}}
		if _, err := validateEnvironment(body); err != nil {
			t.Fatal(err)
		}
		if err := validateCollectionOverrides(Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": body["variables"]}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, rule := range []string{"{{$auto.Client_IP}}", "{{$auto.client_ip()}}", "prefix{{$auto.client_ip}}", "{{$auto.timestamp_ms}}suffix", " {{$auto.client_ip}}", "$auto.client_ip", "{{$auto.timestamp_ms", "{{$random.uuid}}{{$auto.timestamp_ms}}"} {
		if _, _, err := parseEnvironmentRandomRule(rule); err == nil {
			t.Fatalf("malformed/embedded auto directive accepted: %q", rule)
		}
		if _, err := validateEnvironment(Obj{"name": "automatic", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "field", "value": rule}}}); err == nil {
			t.Fatal("invalid auto rule saved", rule)
		}
	}
}

func TestEnvironmentAutomaticResolverPinsOperationAcrossNamesAndBranches(t *testing.T) {
	op := newEnvironmentOperation("::ffff:203.0.113.8", time.UnixMilli(1794212345678))
	resolver := newEnvironmentRuleResolver(nil).withOperation(op)
	values, err := resolver.resolve(map[string]string{"arbitrary_ip": "{{$auto.client_ip}}", "first_clock": "{{$auto.timestamp_ms}}", "second_clock": "{{$auto.timestamp_ms}}", "fixed": "unchanged"})
	if err != nil || values["arbitrary_ip"] != "203.0.113.8" || values["first_clock"] != "1794212345678" || values["first_clock"] != values["second_clock"] {
		t.Fatal("operation was not captured once", values, err)
	}
	collection := Obj{"client_state": Obj{collectionOverridesKey: Obj{"environment-test": Obj{"variables": []any{Obj{"name": "extra_clock", "value": "{{$auto.timestamp_ms}}"}}}}}}
	environment := Obj{"id": "environment-test", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "arbitrary_ip", "value": "{{$auto.client_ip}}"}}}
	merged, err := mergedCollectionVariables(collection, environment, Obj{"fixed": "chosen"}, resolver)
	if err != nil || merged["extra_clock"] != values["first_clock"] || merged["arbitrary_ip"] != values["arbitrary_ip"] || merged["fixed"] != "chosen" {
		t.Fatal("collection operation drifted", merged, err)
	}
	missing := newEnvironmentRuleResolver(nil).withOperation(newEnvironmentOperation("", time.UnixMilli(1794212345678)))
	if _, err = missing.resolve(map[string]string{"field": "{{$auto.client_ip}}"}); err == nil {
		t.Fatal("unknown IP invented a default")
	}
	frozen := Obj{"variables": []any{Obj{"name": "field", "value": "192.0.2.44", "generation_rule": "{{$auto.client_ip}}"}}}
	replay, err := newEnvironmentRuleResolver(frozen).withOperation(op).resolve(map[string]string{"field": "{{$auto.client_ip}}"})
	if err != nil || replay["field"] != "192.0.2.44" {
		t.Fatal("frozen replay regenerated caller IP", replay, err)
	}
}

func TestEnvironmentAutomaticSnapshotRetainsRulesAndHonorsExplicitValues(t *testing.T) {
	operation := newEnvironmentOperation("198.51.100.5", time.UnixMilli(1794212345678))
	resolver := newEnvironmentRuleResolver(nil).withOperation(operation)
	definition := Obj{"name": "automatic", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "source", "value": "{{$auto.client_ip}}", "generation_rule": "forged"}, Obj{"name": "clock", "value": "{{$auto.timestamp_ms}}"}, Obj{"name": "overridden", "value": "{{$auto.client_ip}}"}}}
	snapshot, properties, err := environmentSnapshotWithResolver(definition, resolver, Obj{"overridden": "fixed"})
	if err != nil || properties["source"] != "198.51.100.5" || properties["clock"] != "1794212345678" || properties["overridden"] != "fixed" {
		t.Fatal(snapshot, properties, err)
	}
	if obj(arr(snapshot["variables"])[0])["generation_rule"] != "{{$auto.client_ip}}" || obj(arr(snapshot["variables"])[2])["generation_rule"] != nil {
		t.Fatal("snapshot trusted authored or discarded provenance", snapshot)
	}
	task, err := frozenTaskEnvironmentProperties(Obj{"extra_clock": "{{$auto.timestamp_ms}}"}, snapshot, resolver)
	if err != nil || task["extra_clock"] != properties["clock"] || obj(snapshot["task_property_rules"])["extra_clock"] != "{{$auto.timestamp_ms}}" {
		t.Fatal("explicit task rule used another operation", task, err)
	}
	if obj(arr(definition["variables"])[0])["value"] != "{{$auto.client_ip}}" {
		t.Fatal("runtime changed saved definition")
	}
}
