// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A sample count the service would reject is refused here, before a job is
// submitted and billed.
func TestValidateSampleSize(t *testing.T) {
	assert.NoError(t, validateSampleSize(0), "unset means take the default")
	assert.NoError(t, validateSampleSize(minSampleSize))
	assert.NoError(t, validateSampleSize(maxSampleSize))
	assert.NoError(t, validateSampleSize(100))

	for _, n := range []int{1, minSampleSize - 1, maxSampleSize + 1, -5} {
		err := validateSampleSize(n)
		require.Errorf(t, err, "%d is out of range", n)
		assert.Contains(t, err.Error(), "15")
		assert.Contains(t, err.Error(), "1000", "the message has to name the range it enforces")
	}
}

// --from names a source the service has a path for. A typo caught here costs
// nothing; the same typo reaching the service costs a job.
func TestValidateGenerateSource(t *testing.T) {
	assert.NoError(t, validateGenerateSource(""), "unset means take the default")
	for _, s := range generateSources {
		assert.NoErrorf(t, validateGenerateSource(s), "%q is a documented source", s)
	}

	err := validateGenerateSource("tracez")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracez")
	for _, s := range generateSources {
		assert.Containsf(t, err.Error(), s, "the refusal has to list %q as an option", s)
	}
}

// The generated file lands where `azd ai eval init` scaffolds, so a generated
// dataset is already where an evaluation configuration expects it.
func TestArtifactPath(t *testing.T) {
	assert.Equal(t,
		filepath.Join("evals", "datasets", "support-regression.jsonl"),
		artifactPath(defaultOutputDir, "support-regression"))
	assert.Equal(t,
		filepath.Join("out", "x.jsonl"),
		artifactPath("out", "x"))
}

// The spec's default: traces when the project has Application Insights
// connected, otherwise the agent. The connection string is how a project says
// it collects traces at all, so asking for traces without one would submit a
// billed job against nothing.
func TestDefaultGenerationSource(t *testing.T) {
	assert.Equal(t, []string{generateFromTraces},
		defaultGenerationSource("InstrumentationKey=00000000-0000-0000-0000-000000000000"))
	assert.Equal(t, []string{generateFromAgent}, defaultGenerationSource(""))
}

// The one difference between create and update, and the only thing stopping a
// create from silently publishing version 2 of someone else's dataset.
func TestCheckAssetExistence(t *testing.T) {
	assert.NoError(t, checkAssetExistence("create", "dataset", "x", false))
	assert.NoError(t, checkAssetExistence("update", "dataset", "x", true))

	err := checkAssetExistence("create", "dataset", "x", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update", "the error has to name the verb that works")

	err = checkAssetExistence("update", "dataset", "x", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create")
}

// Generation is billed and the file is checked in, so overwriting one is a
// decision the caller makes rather than a side effect.
func TestRefuseExistingArtifact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "support.jsonl")

	assert.NoError(t, refuseExistingArtifact(path, false), "nothing there yet")

	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))

	err := refuseExistingArtifact(path, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
	assert.Contains(t, err.Error(), "--output-dir", "both ways out have to be named")

	assert.NoError(t, refuseExistingArtifact(path, true), "--force is the way through")
}

// --from is a request, and one the plan cannot honour has to stop the command
// rather than quietly submit a job seeded from less than was asked for.
func TestRefuseUnbuildableSources(t *testing.T) {
	assert.NoError(t, refuseUnbuildableSources(nil))
	assert.NoError(t, refuseUnbuildableSources([]string{}))

	tests := []struct{ kind, says string }{
		{generateFromPrompt, "--agent-instruction"},
		{generateFromAgent, "--target"},
		{generateFromFile, "azd ai dataset create"},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			err := refuseUnbuildableSources([]string{tt.kind})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.says,
				"the error has to name the way out, not just the problem")
		})
	}

	both := refuseUnbuildableSources([]string{generateFromPrompt, generateFromAgent})
	require.Error(t, both)
	assert.Contains(t, both.Error(), "--agent-instruction")
	assert.Contains(t, both.Error(), "--target",
		"two unhonoured sources are two things to fix, so both are reported at once")
}

// The service's system error says only that something went wrong and to try
// again, which sends users into a retry loop against a deterministic failure.
func TestExplainDataGenerationFailure(t *testing.T) {
	systemErr := errors.New("DataGenerationJobSystemError: Something went wrong during data generation")

	explained := explainDataGenerationFailure(systemErr, "support-agent")
	require.Error(t, explained)
	assert.Contains(t, explained.Error(), "support-agent")
	assert.Contains(t, explained.Error(), "--agent-instruction",
		"the explanation has to name the way around it")
	assert.ErrorIs(t, explained, systemErr, "the original must still be reachable")

	assert.NoError(t, explainDataGenerationFailure(nil, "support-agent"))
	assert.Equal(t, systemErr, explainDataGenerationFailure(systemErr, ""),
		"with no agent named there is nothing to explain")

	other := errors.New("connection reset")
	assert.Equal(t, other, explainDataGenerationFailure(other, "support-agent"),
		"an unrelated failure must not be blamed on the agent")
}

func TestIsAgentSeededGenerationFailure(t *testing.T) {
	assert.True(t, isAgentSeededGenerationFailure(errors.New("DataGenerationJobSystemError")))
	assert.True(t, isAgentSeededGenerationFailure(
		errors.New("Something went wrong during data generation")))
	assert.False(t, isAgentSeededGenerationFailure(errors.New("429 Too Many Requests")))
	assert.False(t, isAgentSeededGenerationFailure(nil))
}

// The two job types share an id shape, so reaching for the wrong group is the
// likely mistake and the error has to say where the other one is.
func TestJobLookupErrorPointsAtTheEvaluatorGroup(t *testing.T) {
	err := jobLookupError("dgj_01", errors.New("boom"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dgj_01")

	// A non-404 is reported as itself rather than as a wrong-group guess.
	assert.NotContains(t, err.Error(), "azd ai eval evaluator job")
}

// A window narrows a traces request; its absence is not a request for none.
func TestGenerationPlanTraceOptions(t *testing.T) {
	assert.Nil(t, generationPlan{}.traceOptions())
	assert.Nil(t, generationPlan{TraceDays: -1}.traceOptions())

	opts := generationPlan{TraceDays: 7}.traceOptions()
	require.NotNil(t, opts)
	assert.Equal(t, 7, opts.Days)
}
