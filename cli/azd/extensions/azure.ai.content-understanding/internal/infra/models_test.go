// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type modelFake struct {
	calls    []string
	fail     string
	analyzer string
	catalog  []CatalogModel
}

func modelFixture() *modelFake {
	return &modelFake{analyzer: `{"supportedModels":{"completion":["gpt-5.2"],"embedding":["text-embedding-3-large"]}}`,
		catalog: []CatalogModel{
			{Name: "gpt-5.2", Version: "2026-01", Format: "OpenAI", Default: true,
				SKUs: []ModelSKU{{Name: "Standard", Capacity: 2}, {Name: "GlobalStandard", Capacity: 3}}},
			{Name: "text-embedding-3-large", Version: "1", Format: "OpenAI",
				SKUs: []ModelSKU{{Name: "Standard", Capacity: 0}}},
			{Name: "not-supported", Version: "1", Format: "OpenAI", SKUs: []ModelSKU{{Name: "Standard"}}},
		}}
}
func (f *modelFake) record(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errors.New("private-diagnostic")
	}
	return nil
}
func (f *modelFake) Supported(context.Context) (cuclient.Analyzer, error) {
	return cuclient.Analyzer{JSON: []byte(f.analyzer)}, f.record("supported")
}
func (f *modelFake) Catalog(context.Context) ([]CatalogModel, error) {
	return f.catalog, f.record("catalog")
}
func (f *modelFake) PromptModels(_ context.Context, candidates []Model) ([]Model, error) {
	return candidates, f.record("prompt")
}
func (f *modelFake) Deploy(context.Context, []Model) error { return f.record("deploy") }
func (f *modelFake) ConfigureDefaults(_ context.Context, values map[string]string) error {
	if len(values) != 2 {
		return errors.New("unexpected mappings")
	}
	return f.record("defaults")
}

func TestLiveCandidatesAndExactModelSelection(t *testing.T) {
	f := modelFixture()
	models, err := Candidates(cuclient.Analyzer{JSON: []byte(f.analyzer)}, f.catalog)
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "GlobalStandard", models[0].SKU)
	assert.Equal(t, int32(3), models[0].Capacity)
	assert.Equal(t, int32(1), models[1].Capacity)
	for _, selection := range []string{
		"recommended", "GPT-5.2@2026-01,text-embedding-3-large", "gpt-5.2,text-embedding-3-large@1",
	} {
		selected, err := SelectModels(models, selection)
		require.NoError(t, err)
		assert.Equal(t, models, selected)
	}
	for _, selection := range []string{"unknown", "gpt-5.2@missing", "gpt-5.2,gpt-5.2@2026-01"} {
		_, err := SelectModels(models, selection)
		require.Error(t, err)
	}
	additional := models[0]
	additional.Version, additional.Default = "other", false
	models = append(models, additional)
	selected, err := SelectModels(models, "recommended")
	require.NoError(t, err)
	assert.Equal(t, "2026-01", selected[0].Version)
	_, err = SelectModels(models, "gpt-5.2")
	require.Error(t, err)
	models[0].Default = false
	_, err = SelectModels(models, "recommended")
	require.Error(t, err)
	_, err = SelectModels(nil, "recommended")
	require.Error(t, err)
	for _, wire := range []string{`{}`, `{"supportedModels":null}`, `{"supportedModels":{"completion":"wrong"}}`,
		`{"supportedModels":{"completion":[],"embedding":[]}}`} {
		_, err = Candidates(cuclient.Analyzer{JSON: []byte(wire)}, f.catalog)
		require.Error(t, err)
	}
}

func TestModelSetupOfflineAndSideEffects(t *testing.T) {
	output := filepath.Join(testutil.TempDir(t), "infra", "models.json")
	result, err := SetupModels(t.Context(), nil, " NONE ", output, true, true)
	require.NoError(t, err)
	assert.Empty(t, result.Models)
	assert.False(t, result.Deployed)
	assert.Nil(t, result.DefaultsConfigured)
	body, err := os.ReadFile(output) // #nosec G304 -- Private test output.
	require.NoError(t, err)
	assert.Equal(t, "[]\n", string(body))
	for _, selection := range []string{"recommended", "prompt"} {
		f := modelFixture()
		result, err = SetupModels(t.Context(), f, selection, output, true, true)
		require.NoError(t, err)
		assert.True(t, result.Deployed)
		require.NotNil(t, result.DefaultsConfigured)
		assert.True(t, *result.DefaultsConfigured)
		want := []string{"supported", "catalog"}
		if selection == "prompt" {
			want = append(want, "prompt")
		}
		assert.Equal(t, append(want, "deploy", "defaults"), f.calls)
		body, err = os.ReadFile(output) // #nosec G304 -- Private test output.
		require.NoError(t, err)
		assert.JSONEq(t, `[{"name":"gpt-5.2","model":"gpt-5.2","version":"2026-01","format":"OpenAI",`+
			`"skuName":"GlobalStandard","skuCapacity":3},{"name":"text-embedding-3-large","model":"text-embedding-3-large",`+
			`"version":"1","format":"OpenAI","skuName":"Standard","skuCapacity":1}]`, string(body))
	}
	f := modelFixture()
	result, err = SetupModels(t.Context(), f, "recommended", output, false, false)
	require.NoError(t, err)
	assert.False(t, result.Deployed)
	assert.Equal(t, []string{"supported", "catalog"}, f.calls)
	for _, step := range []string{"supported", "catalog", "prompt", "deploy", "defaults"} {
		f := modelFixture()
		f.fail = step
		output := filepath.Join(testutil.TempDir(t), "models.json")
		_, err := SetupModels(t.Context(), f, "prompt", output, true, true)
		require.Error(t, err)
		_, err = os.Stat(output)
		assert.True(t, os.IsNotExist(err), step)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = SetupModels(ctx, nil, "none", output, true, true)
	assert.ErrorIs(t, err, context.Canceled)
}
