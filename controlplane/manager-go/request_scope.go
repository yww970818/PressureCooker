package main

import "context"

// A request borrows process resources; cancellation and tenant identity belong
// only to its value copy, never the scheduler or another request.
func (p *Platform) forRequest(ctx context.Context, projectID string) *Platform {
	next := p.forProject(projectID)
	next.requestCtx = ctx
	if p.DB != nil {
		next.DB = p.DB.WithContext(ctx)
	}
	return next
}
func (p *Platform) requestContext() context.Context {
	if p.requestCtx != nil {
		return p.requestCtx
	}
	if p.DB == nil {
		return context.Background()
	}
	return p.DB.Context()
}
