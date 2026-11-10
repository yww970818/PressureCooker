package main

import "context"

// Convenience entry points for historical fixtures. Production passes the
// caller context explicitly and does not compile these background wrappers.
func (s *Scheduler) tick() error { return s.tickContext(context.Background()) }

func (p *Platform) cluster() (Obj, error) { return p.clusterContext(context.Background()) }
func (p *Platform) attachClusterStatus(controllers, workers []any) ([]any, []any, error) {
	return p.attachClusterStatusQ(p.DB, controllers, workers)
}
