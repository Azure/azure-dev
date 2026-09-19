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
)
