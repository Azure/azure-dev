// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
)

// Agent represents an AI agent that can execute tools and interact with language models.
// It manages multiple models for different purposes and maintains an executor for tool execution.
type Agent struct {
	debug            bool
	defaultModel     llms.Model
	samplingModel    llms.Model
	executor         *agents.Executor
	tools            []tools.Tool
	callbacksHandler callbacks.Handler
}

// AgentOption is a functional option for configuring an Agent
type AgentOption func(*Agent)

// WithDebug returns an option that enables or disables debug logging for the agent
func WithDebug(debug bool) AgentOption {
	return func(agent *Agent) {
		agent.debug = debug
	}
}

// WithDefaultModel returns an option that sets the default language model for the agent
func WithDefaultModel(model llms.Model) AgentOption {
	return func(agent *Agent) {
		agent.defaultModel = model
	}
}

// WithSamplingModel returns an option that sets the sampling model for the agent
func WithSamplingModel(model llms.Model) AgentOption {
	return func(agent *Agent) {
		agent.samplingModel = model
	}
}

// WithTools returns an option that adds the specified tools to the agent's toolkit
func WithTools(tools ...tools.Tool) AgentOption {
	return func(agent *Agent) {
		agent.tools = tools
	}
}

// WithCallbacksHandler returns an option that sets the callbacks handler for the agent
func WithCallbacksHandler(handler callbacks.Handler) AgentOption {
	return func(agent *Agent) {
		agent.callbacksHandler = handler
	}
}

// toolNames returns a comma-separated string of all tool names in the provided slice
func toolNames(tools []tools.Tool) string {
	var tn strings.Builder
	for i, tool := range tools {
		if i > 0 {
			tn.WriteString(", ")
		}
		tn.WriteString(tool.Name())
	}

	return tn.String()
}

// toolDescriptions returns a formatted string containing the name and description
// of each tool in the provided slice, with each tool on a separate line
func toolDescriptions(tools []tools.Tool) string {
	var ts strings.Builder
	for _, tool := range tools {
		ts.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name(), tool.Description()))
	}

	return ts.String()
}
