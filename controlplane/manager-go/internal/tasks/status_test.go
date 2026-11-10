package tasks

import "testing"

func TestStopSeparatesQueuedExecutionAndRedisDelivery(t *testing.T) {
	for _, tc := range []struct {
		state          Status
		old, requested string
		want           Status
		finish, record bool
		mode           string
	}{
		{Queued, "", "now", Stopped, true, false, ""},
		{Running, "", "graceful", Stopping, false, true, "graceful"},
		{Stopping, "now", "graceful", Stopping, false, true, "now"},
		{Recovering, "", "now", Recovering, false, true, "now"},
		{Uploading, "", "now", Uploading, false, false, ""},
		{CallbackPending, "", "now", CallbackPending, false, false, ""},
		{Completed, "", "now", Completed, false, false, ""},
	} {
		got := tc.state.Stop(tc.old, tc.requested)
		if got.Status != tc.want || got.Finish != tc.finish || got.RecordRequest != tc.record || got.Mode != tc.mode {
			t.Fatalf("%s stop got %+v", tc.state, got)
		}
	}
}

func TestPhysicalRecoveryExcludesQueuedAndDeliveryStates(t *testing.T) {
	for _, tc := range []struct {
		state                      Status
		active, physical, terminal bool
	}{
		{Queued, false, false, false}, {Preparing, true, true, false},
		{Running, true, true, false}, {Stopping, true, true, false},
		{Recovering, true, true, false}, {Uploading, true, false, false},
		{CallbackPending, true, false, false}, {Completed, false, false, true},
		{Stopped, false, false, true}, {Failed, false, false, true},
		{Interrupted, false, false, true}, {"historical-unknown", false, false, false},
	} {
		if tc.state.Active() != tc.active || tc.state.Physical() != tc.physical || tc.state.Terminal() != tc.terminal {
			t.Fatalf("wrong classification for %s", tc.state)
		}
	}
	if got := Running.LostRunner(false); got != Interrupted {
		t.Fatal(got)
	}
	if got := Running.LostRunner(true); got != Stopped {
		t.Fatal(got)
	}
	if got := Stopping.LostRunner(false); got != Stopped {
		t.Fatal(got)
	}
}
