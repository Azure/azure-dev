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
//
// Keys share one schema across extensions. Core adds only "ext."; neither
// extension.id nor extension.event namespaces declarations. Use feature-scoped
// names such as "ext.agent.invoke.protocol". Reuse a key only when its meaning,
// allowed values, classification, and purpose match. Existing generic keys such
// as "ext.source" and "ext.operation" are compatibility exceptions, not naming
// examples for new fields.
package telemetry

import (
	"go.opentelemetry.io/otel/attribute"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

var (
	// AgentPhaseName identifies an extension-owned operation, not a core command span.
	AgentPhaseName = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.name"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseScope distinguishes a project command from a provider method.
	AgentPhaseScope = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.scope"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseAttemptID is a generated opaque ID for one actual invocation.
	AgentPhaseAttemptID = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.attempt_id"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStartTime records the actual UTC start, not the reporting timestamp.
	AgentPhaseStartTime = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.start_time"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseEndTime records the actual UTC return boundary.
	AgentPhaseEndTime = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.end_time"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseDurationMs records inclusive monotonic duration as a string.
	AgentPhaseDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.duration_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseOutcome records success, error, canceled, or timeout for the observed operation.
	AgentPhaseOutcome = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.outcome"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStepID identifies a fixed internal business step.
	AgentPhaseStepID = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.step.id"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStepSequence records start order within one attempt.
	AgentPhaseStepSequence = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.step.sequence"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStepStartOffsetMs records the monotonic offset from the phase start.
	AgentPhaseStepStartOffsetMs = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.step.start_offset_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStepDurationMs records inclusive step elapsed milliseconds as a string.
	AgentPhaseStepDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.step.duration_ms"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentPhaseStepDroppedCount counts local clipping, not delivery or host budget losses.
	AgentPhaseStepDroppedCount = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.phase.step.dropped_count"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}

	// AgentInitStepID identifies a fixed business step in agent Init.
	AgentInitStepID = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.init.step.id"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentInitStepSequence distinguishes repeated steps within one Init invocation.
	AgentInitStepSequence = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.init.step.sequence"),
		Classification: fields.SystemMetadata,
		Purpose:        fields.PerformanceAndHealth,
		Endpoint:       "N/A",
	}
	// AgentInitStepDurationMs records elapsed milliseconds as a string property.
	AgentInitStepDurationMs = fields.AttributeKey{
		Key:            attribute.Key("ext.agent.init.step.duration_ms"),
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
