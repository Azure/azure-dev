// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
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

func TestResponseLegacyNamedCacheRequiresPositivePinEvidence(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, evidence := range []string{"missing", "matching", "conflicting"} {
			t.Run(caller+"/"+evidence, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				cfg.Evals[0].Source = &project.SourceDecl{
					Type: project.SourceTypeResponses, ResponseIDs: []string{"resp_fixed"}, MaxTurns: 1,
				}
				first := reconcileCatalogPin(t, caller, ec, cfg, dir)
				old := service.evals[first]
				require.True(t, responseSchemaMatches(&cfg.Evals[0], old))
				seedLegacyCatalogPinState(t, env, cfg.Evals[0], first)
				if evidence == "missing" {
					old.TestingCriteria = nil
				}
				if evidence != "matching" {
					cfg.Evaluators[0].Version = "2"
				}
				before, err := json.Marshal(old.TestingCriteria)
				require.NoError(t, err)

				next := reconcileCatalogPin(t, caller, ec, cfg, dir)
				wantCreated := 2
				if evidence == "matching" {
					assert.Equal(t, first, next, "complete matching evidence preserves the original history")
					wantCreated = 1
				} else {
					assert.NotEqual(t, first, next, "a compatible response schema cannot establish the immutable pin")
				}
				require.Len(t, service.created, wantCreated)
				current := service.evals[next]
				require.True(t, responseSchemaMatches(&cfg.Evals[0], current))
				require.Len(t, current.TestingCriteria, 1)
				assert.Equal(t, cfg.Evaluators[0].Version, current.TestingCriteria[0].EvaluatorVersion)
				assert.Equal(t, next, reconcileCatalogPin(t, caller, ec, cfg, dir), "unchanged retry must be idempotent")
				assert.Len(t, service.created, wantCreated)
				assert.Equal(t, next, env.stored(t, idKey("eval", cfg.Evals[0].Name)))
				require.Contains(t, service.evals, first)
				after, err := json.Marshal(service.evals[first].TestingCriteria)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after), "the original immutable criteria must not be rewritten")
				assert.Zero(t, service.publishes, "registered evaluator references must not publish new versions")
			})
		}
	}
}
