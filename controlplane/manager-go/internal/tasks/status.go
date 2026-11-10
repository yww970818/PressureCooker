// Package tasks owns task row persistence and execution state policy. Frozen
// dispatches, encrypted environments and protocol payloads remain opaque here.
package tasks

type Status string

const (
	Queued          Status = "QUEUED"
	Preparing       Status = "PREPARING"
	Running         Status = "RUNNING"
	Stopping        Status = "STOPPING"
	Recovering      Status = "RECOVERING"
	Uploading       Status = "UPLOADING"
	CallbackPending Status = "CALLBACK_PENDING"
	Completed       Status = "COMPLETED"
	Failed          Status = "FAILED"
	Stopped         Status = "STOPPED"
	Interrupted     Status = "INTERRUPTED"
)

func (s Status) Terminal() bool {
	switch s {
	case Completed, Failed, Stopped, Interrupted:
		return true
	}
	return false
}

func (s Status) Physical() bool {
	switch s {
	case Preparing, Running, Stopping, Recovering:
		return true
	}
	return false
}

func (s Status) Active() bool { return s.Physical() || s == Uploading || s == CallbackPending }

type StopDecision struct {
	Status        Status
	Finish        bool
	RecordRequest bool
	Mode          string
}

func (s Status) Stop(existingMode, requestedMode string) StopDecision {
	decision := StopDecision{Status: s}
	if s == Uploading || s == CallbackPending {
		return decision
	}
	if s == Queued {
		decision.Status, decision.Finish = Stopped, true
		return decision
	}
	if !s.Active() {
		return decision
	}
	decision.RecordRequest = true
	if s != Recovering && s != Uploading && s != CallbackPending {
		decision.Status = Stopping
	}
	decision.Mode = "graceful"
	if existingMode == "now" || requestedMode == "now" {
		decision.Mode = "now"
	}
	return decision
}

func (s Status) LostRunner(stopRequested bool) Status {
	if stopRequested || s == Stopping {
		return Stopped
	}
	return Interrupted
}
