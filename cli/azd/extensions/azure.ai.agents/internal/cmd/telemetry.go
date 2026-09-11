// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	agentContextResolvedEvent = "agent.context.resolved"
	agentKindAttribute        = "agent.kind"
	agentHarnessAttribute     = "agent.harness"
	agentOperationAttribute   = "agent.operation"

	agentKindUnknown  = "unknown"
	agentHarnessNone  = "none"
	agentHarnessOther = "other"
)

type agentTelemetryContext struct {
	kind      string
	harness   string
	operation string
}

type agentContextReporter struct {
}

func newAgentContextReporter() *agentContextReporter {
	return &agentContextReporter{}
}

func (r *agentContextReporter) reportProject(ctx context.Context, operation string) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		log.Printf("telemetry: failed to create azd client: %v", err)
		return
	}
	defer azdClient.Close()

	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projectResponse.GetProject() == nil {
		return
	}

	r.reportProjectConfig(ctx, azdClient.Telemetry(), projectResponse.GetProject(), operation)
}

func (r *agentContextReporter) reportProjectConfig(
	ctx context.Context,
	telemetry azdext.TelemetryServiceClient,
	project *azdext.ProjectConfig,
	operation string,
) {
	seen := map[string]struct{}{}
	for _, agentCtx := range agentTelemetryContexts(project, operation) {
		key := agentCtx.kind + "\x00" + agentCtx.harness
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		r.report(ctx, telemetry, agentCtx)
	}
}

func (r *agentContextReporter) reportService(
	ctx context.Context,
	telemetry azdext.TelemetryServiceClient,
	project *azdext.ProjectConfig,
	service *azdext.ServiceConfig,
	operation string,
) {
	if project == nil || service == nil {
		return
	}
	project = &azdext.ProjectConfig{Path: project.GetPath(), Services: map[string]*azdext.ServiceConfig{
		service.GetName(): service,
	}}
	r.reportProjectConfig(ctx, telemetry, project, operation)
}

func (r *agentContextReporter) report(
	ctx context.Context,
	telemetry azdext.TelemetryServiceClient,
	agentCtx agentTelemetryContext,
) {
	if telemetry == nil {
		return
	}

	attributes := map[string]string{
		agentKindAttribute:      agentCtx.kind,
		agentHarnessAttribute:   agentCtx.harness,
		agentOperationAttribute: agentCtx.operation,
	}
	if _, err := telemetry.ReportUsage(ctx, &azdext.ReportUsageRequest{
		EventName:  agentContextResolvedEvent,
		Attributes: attributes,
	}); err != nil {
		log.Printf("telemetry: failed to report agent context: %v", err)
	}
}

func agentTelemetryContexts(project *azdext.ProjectConfig, operation string) []agentTelemetryContext {
	if project == nil {
		return nil
	}

	contexts := make([]agentTelemetryContext, 0, len(project.GetServices()))
	for _, svc := range project.GetServices() {
		if svc.GetHost() != AiAgentHost {
			continue
		}

		kind, err := agentkind.Kind(svc, project.GetPath(), "")
		if err != nil {
			kind = agentKindUnknown
		}
		kind = telemetryAgentKind(kind)
		harness := agentHarnessNone
		if kind == string(agent_yaml.AgentKindPrompt) {
			if promptAgent, found, err := projectpkg.PromptAgentFromResolvedService(svc, project.GetPath()); err == nil && found {
				harness = telemetryAgentHarness(promptAgent.HarnessType())
			}
		}

		contexts = append(contexts, agentTelemetryContext{
			kind:      kind,
			harness:   harness,
			operation: operation,
		})
	}
	return contexts
}

func telemetryAgentKind(kind string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(kind)); normalized {
	case string(agent_yaml.AgentKindHosted),
		string(agent_yaml.AgentKindPrompt),
		string(agent_yaml.AgentKindPromptVoice),
		string(agent_yaml.AgentKindVoice),
		string(agent_yaml.AgentKindWorkflow):
		return normalized
	default:
		return agentKindUnknown
	}
}

func telemetryAgentHarness(harness string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(harness)); normalized {
	case "":
		return agentHarnessNone
	case agent_api.ManagedAgentHarnessGitHubCopilot:
		return normalized
	default:
		return agentHarnessOther
	}
}

func telemetryOperation(cmdPath string) string {
	parts := strings.Fields(cmdPath)
	if len(parts) > 0 && parts[0] == "agent" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ".")
}
