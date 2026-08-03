// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The generation spec is keyed by artifact name, which is what makes
// `dataset generate <name>` and `evaluator generate <name>` able to look up
// exactly the entry they were asked for.
func TestLoadGenerateConfig_ParsesTheDocumentedShape(t *testing.T) {
	body := `
generationModel: gpt-5.6-luna
dataset:
  support-agent-smoke:
    sampleSize: 15
    outputDir: ./datasets
evaluator:
  support-quality:
    outputDir: ./evaluators
    deriveFrom: support-agent
`
	path := filepath.Join(t.TempDir(), "generate.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	cfg, err := LoadGenerateConfig(path)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6-luna", cfg.GenerationModel)

	ds, ok := cfg.DatasetSpec("support-agent-smoke")
	require.True(t, ok)
	require.Equal(t, 15, ds.SampleSize)
	require.Equal(t, "./datasets", ds.OutputDir)

	ev, ok := cfg.EvaluatorSpec("support-quality")
	require.True(t, ok)
	require.Equal(t, "./evaluators", ev.OutputDir)
	require.Equal(t, "support-agent", ev.DeriveFrom)
}

// Generation is optional: a developer with hand-authored data and evaluators
// never writes a spec, and the generate commands still run from flags alone.
func TestLoadGenerateConfig_MissingFileIsNotAnError(t *testing.T) {
	cfg, err := LoadGenerateConfig(filepath.Join(t.TempDir(), "generate.yaml"))
	require.NoError(t, err)
	require.Empty(t, cfg.GenerationModel)
	require.Empty(t, cfg.Dataset)

	_, ok := cfg.DatasetSpec("anything")
	require.False(t, ok)
}

// A row count the service would reject costs a billed job to find out about,
// so it is refused at the flag that carried it.
func TestValidateSampleSize(t *testing.T) {
	require.NoError(t, ValidateSampleSize(0), "unset means the default applies")
	require.NoError(t, ValidateSampleSize(MinSampleSize))
	require.NoError(t, ValidateSampleSize(MaxSampleSize))

	require.ErrorContains(t, ValidateSampleSize(MinSampleSize-1), "must be between")
	require.ErrorContains(t, ValidateSampleSize(MaxSampleSize+1), "must be between")
}
