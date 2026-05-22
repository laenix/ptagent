package driver

import "context"

type toolExecContextKey struct{}

// WithToolExecutor binds a per-task tool executor to context.
func WithToolExecutor(ctx context.Context, exec ToolExecutorFunc) context.Context {
	if exec == nil {
		return ctx
	}
	return context.WithValue(ctx, toolExecContextKey{}, exec)
}

func toolExecutorFromContext(ctx context.Context) ToolExecutorFunc {
	if ctx == nil {
		return nil
	}
	if exec, ok := ctx.Value(toolExecContextKey{}).(ToolExecutorFunc); ok {
		return exec
	}
	return nil
}
