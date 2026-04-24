package agent

import "context"

// AgentRunner abstracts pipeline execution so handlers can be unit-tested with mock runners.
type AgentRunner interface {
	RunAsync(ctx context.Context, gctx GameContext) <-chan RunResult
}

// DefaultRunner delegates to the package-level RunAsync function.
type DefaultRunner struct{}

func (DefaultRunner) RunAsync(ctx context.Context, gctx GameContext) <-chan RunResult {
	return RunAsync(ctx, gctx)
}
