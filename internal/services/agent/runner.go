package agent

import "context"

// AgentRunner abstracts pipeline execution so handlers can be unit-tested with mock runners.
type AgentRunner interface {
	Run(ctx context.Context, gctx GameContext) (RunOutput, error)
}

// DefaultRunner delegates to the package-level Run function.
type DefaultRunner struct{}

func (DefaultRunner) Run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	return Run(ctx, gctx)
}
