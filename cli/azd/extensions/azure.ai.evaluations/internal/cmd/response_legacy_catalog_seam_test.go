// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyCatalogPinRenameRejectsResponseSchemaMismatch(t *testing.T) {
	ec, env, service, cfg, dir := newCatalogPinFixture(t)
	cfg.Evals[0].Source = &project.SourceDecl{
		Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
	}
	oldID := reconcileCatalogPin(t, "create", ec, cfg, dir)
	old := service.evals[oldID]
	old.DataSourceConfig = map[string]any{"type": "custom"}
	seedLegacyCatalogPinState(t, env, cfg.Evals[0], oldID)

	cfg.Evals[0].Name = "renamed-before-response-migration"
	effective := withCatalogEvaluatorPins(cfg.Evals[0], cfg)
	effectiveDigest, err := project.FingerprintGroup(effective)
	require.NoError(t, err)
	legacyDigest, err := project.FingerprintGroup(cfg.Evals[0])
	require.NoError(t, err)
	require.NotEqual(t, legacyDigest, effectiveDigest)
	require.Empty(t, env.stored(t, digestIDKey(effectiveDigest)))
	require.Equal(t, oldID, env.stored(t, digestIDKey(legacyDigest)))
	require.Empty(t, env.stored(t, idKey("eval", cfg.Evals[0].Name)))

	reconciler := &evalReconciler{ec: ec}
	require.NoError(t, reconciler.Validate(t.Context(), cfg, dir))
	criteria := reconciler.prepared[cfg.Evals[0].Name].request.TestingCriteria
	require.True(t, matchingEvaluatorPins(old.TestingCriteria, criteria),
		"the legacy candidate must pass the positive identity and pin evidence guard")
	require.False(t, conflictingEvaluatorPins(old.TestingCriteria, criteria))
	require.False(t, responseSchemaMatches(&cfg.Evals[0], old))

	newID := reconcileCatalogPin(t, "create", ec, cfg, dir)
	require.NotEqual(t, oldID, newID)
	assert.Equal(t, "quality", old.Name, "reject the legacy candidate before pushing rename metadata")
	assert.Equal(t, map[string]any{"type": "custom"}, old.DataSourceConfig)
	require.Len(t, service.created, 2)
	assert.Equal(t, "azure_ai_source", service.created[1].DataSourceConfig.Type)
	assert.Equal(t, "responses", service.created[1].DataSourceConfig.Scenario)
	assert.Equal(t, "1", service.created[1].TestingCriteria[0].EvaluatorVersion)
	assert.Equal(t, newID, env.stored(t, digestIDKey(effectiveDigest)))
	assert.Equal(t, oldID, env.stored(t, digestIDKey(legacyDigest)),
		"migration must retain the old history rather than rewrite its legacy identity")
}
