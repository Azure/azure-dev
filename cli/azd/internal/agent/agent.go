// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
)

// agentBase represents an AI agent that can execute tools and interact with language models.
// It manages multiple models for different purposes and maintains an executor for tool execution.
type agentBase struct {
	debug            bool
	defaultModel     llms.Model
	executor         *agents.Executor
	tools            []common.AnnotatedTool
	callbacksHandler callbacks.Handler
	thoughtChan      chan logging.Thought
	cleanupFunc      AgentCleanup
}

type AgentCleanup func() error

type Agent interface {
	SendMessage(ctx context.Context, args ...string) (string, error)
	Stop() error
}

// Stop terminates the agent and performs any necessary cleanup
func (a *agentBase) Stop() error {
	if a.cleanupFunc != nil {
		return a.cleanupFunc()
	}

	return nil
}

// AgentCreateOption is a functional option for configuring an Agent
type AgentCreateOption func(*agentBase)

// WithDebug returns an option that enables or disables debug logging for the agent
func WithDebug(debug bool) AgentCreateOption {
	return func(agent *agentBase) {
		agent.debug = debug
	}
}

// WithDefaultModel returns an option that sets the default language model for the agent
func WithDefaultModel(model llms.Model) AgentCreateOption {
	return func(agent *agentBase) {
		agent.defaultModel = model
	}
}

// WithTools returns an option that adds the specified tools to the agent's toolkit
func WithTools(tools ...common.AnnotatedTool) AgentCreateOption {
	return func(agent *agentBase) {
		agent.tools = tools
	}
}

// WithCallbacksHandler returns an option that sets the callbacks handler for the agent
func WithCallbacksHandler(handler callbacks.Handler) AgentCreateOption {
	return func(agent *agentBase) {
		agent.callbacksHandler = handler
	}
}

func WithThoughtChannel(thoughtChan chan logging.Thought) AgentCreateOption {
	return func(agent *agentBase) {
		agent.thoughtChan = thoughtChan
	}
}

// toolNames returns a comma-separated string of all tool names in the provided slice
func toolNames(tools []common.AnnotatedTool) string {
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
func toolDescriptions(tools []common.AnnotatedTool) string {
	var ts strings.Builder
	for _, tool := range tools {
		ts.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name(), tool.Description()))
	}

	return ts.String()
}
