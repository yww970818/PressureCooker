package protocol

import "testing"

func TestControllerContextNamespaceCannotBeUserProperties(t *testing.T) {
	for _, key := range []string{"pressurecooker.runtime.workers", "PRESSURECOOKER.RUNTIME.TASK_ID", "pressurecooker.runtime.future"} {
		if !RuntimePropertyReserved(key) {
			t.Fatalf("trusted controller namespace was user writable: %s", key)
		}
	}
	for _, key := range []string{"custom_count", "custom_delay_ms", "pressurecooker.runtime_label", "pressurecooker.custom"} {
		if RuntimePropertyReserved(key) {
			t.Fatalf("unrelated user property rejected: %s", key)
		}
	}
}
