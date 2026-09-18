// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// provisionLayerTelemetry accumulates layer metrics as their values become known.
// Configuration-derived fields are always available. Topology fields remain unset
// until dependency analysis succeeds, except for zero- and single-layer runs where
// the topology is trivial.
type provisionLayerTelemetry struct {
	// isV2 is true when the project uses top-level `layers:` instead of legacy `infra.layers:`.
	isV2 bool

	// layerCount is the number of infrastructure entries that this provision run will process.
	layerCount int

	// explicitDependsOnCount is the number of owning layers that declare at least one `dependsOn` edge.
	explicitDependsOnCount int

	// maxParallel is the widest dependency level: the most infrastructure entries that could run at once.
	// nil means dependency analysis did not complete; a pointer to zero is a known zero-layer result.
	maxParallel *int

	// safeFallbackCount is the number of entries serialized because static analysis could not safely determine
	// their dependencies. nil means analysis did not complete; a pointer to zero means no fallback was needed.
	safeFallbackCount *int
}

func newProvisionLayerTelemetry(
	projectConfig *project.ProjectConfig,
	layers []provisioning.Options,
) *provisionLayerTelemetry {
	explicitDependsOnCount := 0
	if projectConfig.Format() == project.ProjectFormatLayersV2 {
		for _, layer := range projectConfig.Layers {
			if len(layer.DependsOn) > 0 {
				explicitDependsOnCount++
			}
		}
	} else {
		for _, layer := range projectConfig.Infra.Layers {
			if len(layer.DependsOn) > 0 {
				explicitDependsOnCount++
			}
		}
	}

	telemetry := &provisionLayerTelemetry{
		isV2:                   projectConfig.Format() == project.ProjectFormatLayersV2,
		layerCount:             len(layers),
		explicitDependsOnCount: explicitDependsOnCount,
	}
	if len(layers) <= 1 {
		telemetry.maxParallel = new(len(layers))
		telemetry.safeFallbackCount = new(0)
	}
	return telemetry
}

func (t *provisionLayerTelemetry) setDependencies(deps *bicep.LayerDependencies) {
	if deps == nil {
		return
	}

	maxParallel := min(t.layerCount, 1)
	for _, level := range deps.Levels {
		maxParallel = max(maxParallel, len(level))
	}
	t.maxParallel = new(maxParallel)
	t.safeFallbackCount = new(len(deps.SafeFallbackLayers))
}

// emit attaches the layer metrics currently known to the ambient provision command span.
//
// Emits:
//
//   - provision.layer.is_v2                        — top-level layers format
//   - provision.layer.count                        — provisioning infrastructure entries
//   - provision.layer.explicit_dependson_count     — layers using dependsOn
//   - provision.layer.max_parallel                 — largest dependency level, when known
//   - provision.layer.safe_fallback_count          — layers with hasUnknown, when known
//
// All attributes are SystemMetadata (a format flag and counts only, no
// template content), so they're collected without any user-facing opt-in
// beyond the existing telemetry consent.
func (t *provisionLayerTelemetry) emit(ctx context.Context) {
	tracing.SetAttributesInContext(
		ctx,
		fields.ProvisionLayerIsV2Key.Bool(t.isV2),
		fields.ProvisionLayerCountKey.Int(t.layerCount),
		fields.ProvisionLayerExplicitDependsOnCountKey.Int(t.explicitDependsOnCount),
	)

	// Both values come from dependency analysis. If analysis did not finish, leave them out rather than
	// reporting zeros that would look like real results.
	if t.maxParallel != nil && t.safeFallbackCount != nil {
		tracing.SetAttributesInContext(
			ctx,
			fields.ProvisionLayerMaxParallelKey.Int(*t.maxParallel),
			fields.ProvisionLayerSafeFallbackCountKey.Int(*t.safeFallbackCount),
		)
	}
}
