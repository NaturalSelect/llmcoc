// NOTE: Defines AI agent roles and their interactions.
package agent

import "context"

// AgentRunner 抽象KP主流程与白字描述生成,便于handler做单元测试。
type AgentRunner interface {
	Run(ctx context.Context, gctx GameContext) (RunOutput, error)
	RunWriter(ctx context.Context, gctx GameContext, direction string, nsfw bool) (string, error)
	RunWriterStream(ctx context.Context, gctx GameContext, direction string, nsfw bool, onToken func(string)) (string, error)
	RunPainter(ctx context.Context, gctx GameContext, request ImagePromptRequest) (string, error)
}

// DefaultRunner 使用默认agent编排实现。
type DefaultRunner struct{}

func (DefaultRunner) Run(ctx context.Context, gctx GameContext) (RunOutput, error) {
	return Run(ctx, gctx)
}

func (DefaultRunner) RunWriter(ctx context.Context, gctx GameContext, direction string, nsfw bool) (string, error) {
	return RunWriter(ctx, gctx, direction, nsfw)
}

func (DefaultRunner) RunWriterStream(ctx context.Context, gctx GameContext, direction string, nsfw bool, onToken func(string)) (string, error) {
	return RunWriterStream(ctx, gctx, direction, nsfw, onToken)
}

func (DefaultRunner) RunPainter(ctx context.Context, gctx GameContext, request ImagePromptRequest) (string, error) {
	return RunPainter(ctx, gctx, request)
}
