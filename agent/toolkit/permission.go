package toolkit

import (
	"context"
	"fmt"

	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// PermissionMode controls whether a tool may run without host approval.
type PermissionMode int

const (
	// PermissionAuto runs the tool without asking. Suitable for read-only,
	// zero-side-effect tools such as calculator or datetime.
	PermissionAuto PermissionMode = iota
	// PermissionAsk defers the decision to the host application through the
	// PermissionFunc callback. Suitable for side-effecting tools such as file
	// writes or SQL mutations.
	PermissionAsk
	// PermissionDeny blocks the tool from running.
	PermissionDeny
)

// PermissionRequest describes a pending tool invocation presented to the host
// application for approval. The SDK never renders UI; the host decides.
type PermissionRequest struct {
	ToolName string
	Input    any
	Step     int
}

// PermissionFunc lets the host application approve or reject a tool call. It is
// only consulted in PermissionAsk mode. Returning false blocks execution.
type PermissionFunc func(ctx context.Context, request PermissionRequest) (allow bool, err error)

// Permission is the policy applied to a tool before it executes.
type Permission struct {
	Mode PermissionMode
	// Ask is invoked when Mode is PermissionAsk. If nil in Ask mode, the call
	// is denied to fail safe.
	Ask PermissionFunc
}

// permissionMiddleware enforces the permission policy around a tool's Execute.
func permissionMiddleware(name string, permission Permission) Middleware {
	return func(_ string, next agent.ToolExecuteFunc) agent.ToolExecuteFunc {
		return func(ctx context.Context, input any, execution agent.ToolExecutionContext) (any, error) {
			switch permission.Mode {
			case PermissionDeny:
				return nil, fmt.Errorf("tool %q denied by permission policy", name)
			case PermissionAsk:
				if permission.Ask == nil {
					return nil, fmt.Errorf("tool %q requires approval but no permission handler is configured", name)
				}
				allow, err := permission.Ask(ctx, PermissionRequest{
					ToolName: name,
					Input:    input,
					Step:     execution.Step,
				})
				if err != nil {
					return nil, fmt.Errorf("tool %q permission check failed: %w", name, err)
				}
				if !allow {
					return nil, fmt.Errorf("tool %q rejected by permission handler", name)
				}
			case PermissionAuto:
				// no gate
			}
			return next(ctx, input, execution)
		}
	}
}
