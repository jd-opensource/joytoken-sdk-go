package toolkit

import (
	"context"
	"fmt"
	"time"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// Middleware wraps a tool's Execute function to add cross-cutting behavior such
// as timeouts, auditing, or sandboxing. The tool name is provided so a single
// middleware can vary its behavior per tool. Middleware registered first is the
// outermost layer.
type Middleware func(name string, next agent.ToolExecuteFunc) agent.ToolExecuteFunc

// Timeout returns middleware that bounds each tool call to the given duration.
// A non-positive duration disables the timeout.
func Timeout(d time.Duration) Middleware {
	return func(name string, next agent.ToolExecuteFunc) agent.ToolExecuteFunc {
		return func(ctx context.Context, input any, execution agent.ToolExecutionContext) (any, error) {
			if d <= 0 {
				return next(ctx, input, execution)
			}
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()

			type result struct {
				value any
				err   error
			}
			done := make(chan result, 1)
			go func() {
				// Recover here too: this runs in a separate goroutine, so a
				// panic would otherwise crash the process rather than surface
				// as a tool error the agent can feed back to the model.
				defer func() {
					if r := recover(); r != nil {
						done <- result{err: fmt.Errorf("tool %q panicked: %v", name, r)}
					}
				}()
				value, err := next(ctx, input, execution)
				done <- result{value: value, err: err}
			}()

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("tool %q timed out after %s: %w", name, d, ctx.Err())
			case r := <-done:
				return r.value, r.err
			}
		}
	}
}

// Audit returns middleware that reports each tool invocation and its outcome
// through the provided callback. The callback must not block for long; it runs
// inline before and after the tool executes.
func Audit(log func(name string, input any, err error)) Middleware {
	return func(name string, next agent.ToolExecuteFunc) agent.ToolExecuteFunc {
		return func(ctx context.Context, input any, execution agent.ToolExecutionContext) (any, error) {
			value, err := next(ctx, input, execution)
			if log != nil {
				log(name, input, err)
			}
			return value, err
		}
	}
}
