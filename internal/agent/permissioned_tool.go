package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/permission"
)

// permissionedTool gates a tool call before the tool runs. Putting the
// check here rather than inside each tool means a new tool cannot
// forget it: an unmapped tool is refused rather than waved through.
type permissionedTool struct {
	inner       fantasy.AgentTool
	permissions permission.Service
	workingDir  string
}

func newPermissionedTool(inner fantasy.AgentTool, permissions permission.Service, workingDir string) *permissionedTool {
	return &permissionedTool{inner: inner, permissions: permissions, workingDir: workingDir}
}

// wrapToolsWithPermissions gates every tool in the slice. It runs
// before the hook wrapper is applied, so the finished chain is
// hooks -> permissions -> tool and a hook's allow decision is already
// on the context when the gate looks for it.
func wrapToolsWithPermissions(agentTools []fantasy.AgentTool, permissions permission.Service, workingDir string) []fantasy.AgentTool {
	if permissions == nil {
		return agentTools
	}
	out := make([]fantasy.AgentTool, len(agentTools))
	for i, tool := range agentTools {
		out[i] = newPermissionedTool(tool, permissions, workingDir)
	}
	return out
}

func (p *permissionedTool) Info() fantasy.ToolInfo {
	return p.inner.Info()
}

func (p *permissionedTool) ProviderOptions() fantasy.ProviderOptions {
	return p.inner.ProviderOptions()
}

func (p *permissionedTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	p.inner.SetProviderOptions(opts)
}

func (p *permissionedTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	access, ok := tools.AccessOfTool(p.inner, call.Name, call.Input, p.workingDir)
	if !ok {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"Permission denied: %q requests an access the permission system cannot describe, so it cannot be approved.",
			call.Name,
		)), nil
	}

	sessionID := tools.GetSessionFromContext(ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, fmt.Errorf("session ID is required to run %s", call.Name)
	}

	// A denied call must be refused before anything reads the file it
	// names: planning opens files and queries language servers, and
	// doing that for a path the configuration forbids would leak the
	// very thing the rule protects.
	if decision, denied := p.permissions.PolicyDenial(access); denied {
		return tools.DecisionResponse(decision), nil
	}

	if planner, ok := p.inner.(tools.Planner); ok {
		return p.runPlanned(ctx, planner, call, sessionID, access)
	}

	decision := p.permissions.Gate(ctx, permission.GateRequest{
		SessionID:  sessionID,
		ToolCallID: call.ID,
		Access:     access,
		Preview:    tools.PreviewOfTool(p.inner, call.Name, call.Input, p.workingDir),
	})
	if !decision.Allowed() {
		return tools.DecisionResponse(decision), nil
	}
	return p.inner.Run(ctx, call)
}

// runPlanned works the call out, shows the user what it would do, and
// carries it out only if they agree.
func (p *permissionedTool) runPlanned(
	ctx context.Context,
	planner tools.Planner,
	call fantasy.ToolCall,
	sessionID string,
	access permission.Access,
) (fantasy.ToolResponse, error) {
	plan, err := planner.Plan(ctx, call)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if plan.Response != nil {
		return *plan.Response, nil
	}

	decision := p.permissions.Gate(ctx, permission.GateRequest{
		SessionID:  sessionID,
		ToolCallID: call.ID,
		Access:     access,
		Preview:    plan.Preview,
	})
	if decision.Outcome != permission.OutcomeAllow {
		resp := tools.DecisionResponse(decision)
		if plan.Refusal != nil {
			resp = fantasy.WithResponseMetadata(resp, plan.Refusal)
		}
		return resp, nil
	}

	return plan.Apply(ctx)
}
