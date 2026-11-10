package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pressurecooker/execution/protocol"
)

func TestPreparedPropertiesFreezeSignedWorkerOrderAndTask(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := service{c: config{work: base}, s: mockStore(t)}
	task := protocol.Task{
		TaskID: "run-runtime", AttemptID: "attempt-runtime", ProjectID: "project-runtime",
		Workers:    []protocol.Worker{{ID: "worker-3"}, {ID: "worker-1"}},
		Properties: map[string]string{"custom_count": "3", "user_key": "unchanged"},
		Plan:       seed(t, s.s, "runtime-plan.jmx", `<jmeterTestPlan><hashTree><TestPlan/><hashTree/></hashTree></jmeterTestPlan>`),
	}
	before := map[string]string{"custom_count": "3", "user_key": "unchanged"}
	if _, _, err = s.prepareInputs(context.Background(), task, "lease-runtime"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.taskDir(task.TaskID), "task.properties"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`pressurecooker.runtime.workers=["worker-3","worker-1"]`, "pressurecooker.runtime.task_id=run-runtime", "custom_count=3", "user_key=unchanged"} {
		if !strings.Contains(string(raw), want+"\n") {
			t.Fatalf("missing frozen runtime property %q", want)
		}
	}
	if !reflect.DeepEqual(task.Properties, before) {
		t.Fatal("signed task properties mutated")
	}
}

func TestPreparedPropertiesRejectForgedRuntimeContext(t *testing.T) {
	for _, key := range []string{"pressurecooker.runtime.workers", "PressureCooker.Runtime.Task_ID", "pressurecooker.runtime.other"} {
		t.Run(key, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := service{c: config{work: base}, s: mockStore(t)}
			task := protocol.Task{TaskID: "run-runtime", AttemptID: "attempt-runtime", ProjectID: "project-runtime", Workers: []protocol.Worker{{ID: "worker-1"}}, Properties: map[string]string{key: "forged"}, Plan: seed(t, s.s, "forged-plan.jmx", `<jmeterTestPlan><hashTree><TestPlan/><hashTree/></hashTree></jmeterTestPlan>`)}
			if _, _, err = s.prepareInputs(context.Background(), task, "lease-runtime"); err == nil {
				t.Fatal("client runtime context accepted")
			}
		})
	}
}
