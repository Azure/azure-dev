// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry declares the classification metadata for attributes emitted
// by first-party azd extensions and verifies, at test time, that every attribute
// an extension reports is declared here.
//
// Extensions record usage by constructing a telemetry payload — a foundry
// telemetry Event or an azdext ReportUsageRequest — with an inline Attributes
// map. scanExtensionTelemetry parses extension source with go/parser (it never
// executes extension code), finds those payload literals, and collects their
// attribute keys; validateExtensionTelemetryUsages then checks each key against
// the declarations in this file.
package telemetry

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

var (
	// InitFlowVersion identifies the fixed setup timing schema version.
	InitFlowVersion = fields.AttributeKey{
		Key:            attribute.Key("ext.init.flow.version"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitFlowScope identifies the command owning the timing sequence and summary.
	InitFlowScope = fields.AttributeKey{
		Key:            attribute.Key("ext.init.flow.scope"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitFlowOutcome records success, error, canceled, or timeout for setup.
	InitFlowOutcome = fields.AttributeKey{
		Key:            attribute.Key("ext.init.flow.outcome"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepID identifies a fixed setup business step.
	InitStepID = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.id"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepCategory distinguishes prompt, execution, and external setup work.
	InitStepCategory = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.category"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepSequence records the step start order within one extension command.
	InitStepSequence = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.sequence"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepOutcome records success, error, canceled, or timeout for one attempt.
	InitStepOutcome = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.outcome"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepInputSource records user, flag, config, environment, or default when known.
	InitStepInputSource = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.input_source"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepTimingScope distinguishes operations, prompt RPCs, and composite prompt RPCs.
	InitStepTimingScope = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.timing_scope"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepDurationMs records monotonic elapsed milliseconds, encoded as a string by the extension API.
	InitStepDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.duration_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitPromptCount counts simple interactive prompt RPC attempts, including cancellations.
	InitPromptCount = fields.AttributeKey{
		Key:            attribute.Key("ext.init.prompt.count"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitPromptTotalDurationMs records the sum of simple interactive prompt RPC durations.
	InitPromptTotalDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.init.prompt.total_duration_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitPromptLongestID identifies the longest simple prompt, or none if there were no prompts.
	InitPromptLongestID = fields.AttributeKey{
		Key:            attribute.Key("ext.init.prompt.longest.id"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitPromptLongestDurationMs records the longest simple interactive prompt RPC duration.
	InitPromptLongestDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.init.prompt.longest_duration_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepCount counts all timing attempts started in one extension command.
	InitStepCount = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.count"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// InitStepDroppedCount counts completed steps exceeding the local timing buffer.
	InitStepDroppedCount = fields.AttributeKey{
		Key:            attribute.Key("ext.init.step.dropped_count"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}

	// DemoMode records the fixed mode used by the demo extension telemetry example.
	DemoMode = fields.AttributeKey{
		Key:            attribute.Key("ext.demo.mode"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// DemoOutcome records the fixed outcome used by the demo extension telemetry example.
	DemoOutcome = fields.AttributeKey{
		Key:            attribute.Key("ext.demo.outcome"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentKind records the bounded agent kind resolved by azure.ai.agents.
	AgentKind = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.kind"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentHarness records the bounded agent harness classification resolved by azure.ai.agents.
	AgentHarness = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.harness"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentOperation records the fixed extension command path associated with an agent context.
	AgentOperation = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.operation"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentInvokeProtocol records the selected remote hosted-agent invocation protocol.
	AgentInvokeProtocol = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.invoke.protocol"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentInvokeLongRunning records whether the remote invoke requested background execution.
	AgentInvokeLongRunning = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.invoke.long_running"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentInvokeNoWait records whether the remote invoke returns after receiving its ID.
	AgentInvokeNoWait = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.invoke.no_wait"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// AgentContainerMode records the effective hosted-agent container or code mode.
	AgentContainerMode = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.container.mode"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// LocalClientRoute records the bounded local-client route selected by azure.ai.agents.
	LocalClientRoute = fields.AttributeKey{
		Key:            attribute.Key("ext.route"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// InspectorFunnelStage records the bounded Agent Inspector funnel stage.
	InspectorFunnelStage = fields.AttributeKey{
		Key:            attribute.Key("ext.stage"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// InspectorFunnelOutcome records the bounded Agent Inspector funnel outcome.
	InspectorFunnelOutcome = fields.AttributeKey{
		Key:            attribute.Key("ext.outcome"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// DatasetOperation records the bounded publish operation reported by azure.ai.dataset.
	DatasetOperation = fields.AttributeKey{
		Key:            attribute.Key("ext.operation"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}

	// EvaluationsInitSource records the bounded init source reported by azure.ai.evaluations.
	EvaluationsInitSource = fields.AttributeKey{
		Key:            attribute.Key("ext.source"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.FeatureInsight,
		Endpoint:       "N/A",
	}
)
