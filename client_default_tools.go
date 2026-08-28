package joytoken

import "github.com/jd-opensource/joytoken-sdk-go/tooldef"

// defaultLocalToolSet returns the safe, zero-config local tools injected by
// default into the RunChatCompletion loop. They are defined in
// the tooldef package (the bottom of the dependency graph) so this root client
// can reuse the exact same implementations the agent toolkit exposes, without
// an import cycle. Order is stable so tool listing is deterministic.
func defaultLocalToolSet() []Tool {
	return []Tool{
		tooldef.Calculator(),
		tooldef.DateTime(),
	}
}

// resolvedTools returns the executable tool set selected for calls that do not
// carry request-level tool declarations. Caller-registered tools are an
// explicit replacement for the defaults: once WithTools/WithToolHandler is
// used, only that user-owned set is returned. Defaults are injected only when
// the caller supplied no tools at either level.
//
// The default local set is the zero-config compute tools (calculator, datetime)
// plus the workspace file tools and the shell tool. All are always declared to
// the model: file_search, list_dir and file_read run freely (read-only,
// sandbox-confined), while file_write and shell are gated at execution time via
// WithFilePermission / WithShellPermission. With no callback configured, those
// side-effecting tools are still declared but refuse to run until approval.
func (c *Client) resolvedTools() (map[string]Tool, []string) {
	byName := make(map[string]Tool, len(c.tools)+len(defaultLocalToolSet()))
	order := make([]string, 0, len(c.toolOrder)+2)

	for _, name := range c.toolOrder {
		byName[name] = c.tools[name]
		order = append(order, name)
	}
	if len(order) > 0 {
		return byName, order
	}

	if c.defaultLocalTools {
		defaults := defaultLocalToolSet()
		defaults = append(defaults, c.defaultFileTools()...)
		defaults = append(defaults, c.defaultShellTools()...)
		for _, t := range defaults {
			if _, exists := byName[t.Name]; exists {
				continue
			}
			if c.excludedDefaultTools[t.Name] {
				continue
			}
			byName[t.Name] = t
			order = append(order, t.Name)
		}
	}

	return byName, order
}

// registeredToolHandlers returns only caller-registered executable tools whose
// names are present in allowed. It intentionally never falls back to SDK
// defaults, preventing a request-level declaration such as "calculator" from
// being captured by the SDK's same-named default implementation.
func (c *Client) registeredToolHandlers(allowed map[string]bool) map[string]Tool {
	handlers := make(map[string]Tool, len(allowed))
	for name := range allowed {
		if tool, ok := c.tools[name]; ok {
			handlers[name] = tool
		}
	}
	return handlers
}
