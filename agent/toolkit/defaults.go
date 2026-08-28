package toolkit

import (
	"github.com/jd-opensource/joytoken-sdk-go/agent"
)

// WithDefaults implements convention-over-configuration for agent tools:
// when the host application has not configured any tools (options.Tools is
// nil), it injects the safe default tool set. An explicitly empty, non-nil
// slice preserves the host's intent to run with no tools at all.
//
// It returns a copy of options with Tools populated as needed, so it does not
// mutate the caller's value. The agent package stays free of any dependency on
// toolkit, keeping the dependency direction one-way (toolkit -> agent).
//
// Usage:
//
//	opts := toolkit.WithDefaults(agent.AgentOptions{Model: provider})
//	a := agent.New(opts)
func WithDefaults(options agent.AgentOptions, toolkitOptions ...Option) agent.AgentOptions {
	if options.Tools == nil {
		options.Tools = Default(toolkitOptions...).Tools()
	}
	return options
}

// NewAgent is a convenience constructor equivalent to
// agent.New(toolkit.WithDefaults(options, toolkitOptions...)).
func NewAgent(options agent.AgentOptions, toolkitOptions ...Option) *agent.Agent {
	return agent.New(WithDefaults(options, toolkitOptions...))
}
