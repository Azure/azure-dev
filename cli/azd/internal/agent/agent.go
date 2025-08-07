package agent

import (
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
)

type Agent struct {
	debug            bool
	defaultModel     llms.Model
	samplingModel    llms.Model
	executor         *agents.Executor
	tools            []tools.Tool
	callbacksHandler callbacks.Handler
}

type AgentOption func(*Agent)

func WithDebug(debug bool) AgentOption {
	return func(agent *Agent) {
		agent.debug = debug
	}
}

func WithDefaultModel(model llms.Model) AgentOption {
	return func(agent *Agent) {
		agent.defaultModel = model
	}
}

func WithSamplingModel(model llms.Model) AgentOption {
	return func(agent *Agent) {
		agent.samplingModel = model
	}
}

func WithTools(tools ...tools.Tool) AgentOption {
	return func(agent *Agent) {
		agent.tools = tools
	}
}

func WithCallbacksHandler(handler callbacks.Handler) AgentOption {
	return func(agent *Agent) {
		agent.callbacksHandler = handler
	}
}

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

func toolDescriptions(tools []tools.Tool) string {
	var ts strings.Builder
	for _, tool := range tools {
		ts.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name(), tool.Description()))
	}

	return ts.String()
}
