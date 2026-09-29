// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"
	"strings"
	"sync"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/containerref"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const (
	agentContextResolvedEvent   = "agent.context.resolved"
	agentKindAttribute          = "agent.kind"
	agentHarnessAttribute       = "agent.harness"
	agentOperationAttribute     = "agent.operation"
	agentContainerModeAttribute = "agent.container.mode"

	containerModeBuild           = "build"
	containerModeCode            = "code"
	containerModePassthrough     = "passthrough"
	containerModePassthroughAuth = "passthrough_auth"
	containerModeUnknown         = "unknown"

	agentKindUnknown  = "unknown"
	agentHarnessNone  = "none"
	agentHarnessOther = "other"
)

type agentTelemetryContext struct {
	kind          string
	harness       string
	operation     string
	containerMode string // empty for non-hosted agents
}

type agentContextReporter struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newAgentContextReporter() *agentContextReporter {
	return &agentContextReporter{seen: map[string]struct{}{}}
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
	telemetry v1beta.TelemetryServiceClient,
	project *azdext.ProjectConfig,
	operation string,
) {
	for _, agentCtx := range agentTelemetryContexts(project, operation) {
		key := agentCtx.kind + "\x00" + agentCtx.harness + "\x00" + agentCtx.containerMode
		r.mu.Lock()
		_, exists := r.seen[key]
		if !exists {
			r.seen[key] = struct{}{}
		}
		r.mu.Unlock()
		if exists {
			continue
		}
		r.report(ctx, telemetry, agentCtx)
	}
}

func (r *agentContextReporter) reportService(
	ctx context.Context,
	telemetry v1beta.TelemetryServiceClient,
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
	telemetry v1beta.TelemetryServiceClient,
	agentCtx agentTelemetryContext,
) {
	if telemetry == nil {
		return
	}

	var err error
	if agentCtx.containerMode != "" {
		_, err = telemetry.ReportUsage(ctx, &v1beta.ReportUsageRequest{
			EventName: agentContextResolvedEvent,
			Attributes: map[string]string{
				agentKindAttribute:          agentCtx.kind,
				agentHarnessAttribute:       agentCtx.harness,
				agentOperationAttribute:     agentCtx.operation,
				agentContainerModeAttribute: agentCtx.containerMode,
			},
		})
	} else {
		_, err = telemetry.ReportUsage(ctx, &v1beta.ReportUsageRequest{
			EventName: agentContextResolvedEvent,
			Attributes: map[string]string{
				agentKindAttribute:      agentCtx.kind,
				agentHarnessAttribute:   agentCtx.harness,
				agentOperationAttribute: agentCtx.operation,
			},
		})
	}
	if err != nil {
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

		containerMode := ""
		if kind == string(agent_yaml.AgentKindHosted) {
			containerMode = telemetryContainerMode(svc, project.GetPath())
		}

		contexts = append(contexts, agentTelemetryContext{
			kind:          kind,
			harness:       harness,
			operation:     operation,
			containerMode: containerMode,
		})
	}
	return contexts
}

// telemetryContainerMode classifies the effective hosted definition without emitting
// image references or connection identifiers. A legacy image without explicit
// passthrough can take either the build or pre-built path, so it stays unknown.
func telemetryContainerMode(svc *azdext.ServiceConfig, projectRoot string) string {
	agentDef, isHosted, _, err := projectpkg.LoadAgentDefinition(svc, projectRoot)
	if err != nil || !isHosted {
		return containerModeUnknown
	}
	if agentDef.CodeConfiguration != nil {
		return containerModeCode
	}
	image := strings.TrimSpace(agentDef.Image)
	// Legacy disk definitions do not incorporate a service-level image override.
	if image == "" && strings.TrimSpace(svc.GetImage()) != "" {
		return containerModeUnknown
	}
	connection := strings.TrimSpace(agentDef.RegistryConnectionID)
	if svc.GetDocker().GetImagePassthrough() {
		if image == "" || (agentDef.RegistryConnectionID != "" && connection == "") ||
			svc.GetDocker().GetRemoteBuild() {
			return containerModeUnknown
		}
		if connection != "" {
			if !containerref.IsFullyQualified(image) {
				return containerModeUnknown
			}
			return containerModePassthroughAuth
		}
		return containerModePassthrough
	}
	if image != "" || connection != "" {
		return containerModeUnknown
	}
	return containerModeBuild
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
