// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const savedEnvelope = `{"status":"Succeeded","result":{"analyzerId":"prebuilt-image","contents":[{"kind":"image",` +
	`"mimeType":"image/png","markdown":"Picture"}]},"usage":{"tokens":{"model":9007199254740993}}}`

func savedResultNotice(input string) string {
	return "Saved complete analysis result to " + input + analysis.ResultSuffix + ".\n"
}

func cachedResultNotice(input string) string {
	return "Using cached analysis result from " + input + analysis.ResultSuffix + "; pass --refresh to reanalyze.\n"
}

func requireLocalCode(t *testing.T, err error, code string) *azdext.LocalError {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a local error, got %v", err)
	assert.Equal(t, code, local.Code)
	return local
}

func savedResultFixture(t *testing.T, response string) (*analyzeFixture, string) {
	t.Helper()
	f := newAnalyzeFixture(t)
	f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(response)}, nil
	}
	return f, filepath.Join(testutil.TempDir(t), "photo.png")
}

func TestAnalyzeSavesAndReusesCompleteResult(t *testing.T) {
	f, input := savedResultFixture(t, savedEnvelope)
	require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
	online, stderr, err := f.execute(t.Context(), input, "--pretty=false")
	require.NoError(t, err)
	assert.Equal(t, savedResultNotice(input), stderr)
	assert.Contains(t, online, "Picture")
	// #nosec G304 -- Reads only this test's private temporary result.
	saved, err := os.ReadFile(input + analysis.ResultSuffix)
	require.NoError(t, err)
	assert.JSONEq(t, savedEnvelope, string(saved), "Markdown presentation must still save the complete response")
	assert.Contains(t, string(saved), "9007199254740993")

	for _, tt := range []struct {
		name  string
		flags []string
		check func(t *testing.T, stdout string)
	}{
		{"markdown", []string{"--pretty=false"}, func(t *testing.T, stdout string) { assert.Equal(t, online, stdout) }},
		{"json", []string{"-o", "json", "--pretty=false"}, func(t *testing.T, stdout string) {
			assert.JSONEq(t, savedEnvelope, stdout)
			assert.Contains(t, stdout, "9007199254740993")
		}},
		{"json usage", []string{"-o", "json", "--usage", "--pretty=false"}, func(t *testing.T, stdout string) {
			assert.JSONEq(t, `{"result":`+savedEnvelope+`,"usage":{"tokens":{"model":9007199254740993}}}`, stdout)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := f.execute(t.Context(), append([]string{input}, tt.flags...)...)
			require.NoError(t, err)
			assert.Equal(t, cachedResultNotice(input), stderr)
			tt.check(t, stdout)
			assert.EqualValues(t, 1, f.client.calls.Load(), "a saved result must not issue another analysis")
		})
	}
}

func TestAnalyzeRefreshReplacesOnlyAfterSuccess(t *testing.T) {
	f, input := savedResultFixture(t, savedEnvelope)
	require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
	_, _, err := f.execute(t.Context(), input)
	require.NoError(t, err)

	f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{}, &cuclient.Error{
			Kind: cuclient.ErrorService, Code: "ServiceFailed", Message: "Safe service failure.",
		}
	}
	_, _, err = f.execute(t.Context(), input, "--refresh")
	require.Error(t, err)
	// #nosec G304 -- Reads only this test's private temporary result.
	saved, err := os.ReadFile(input + analysis.ResultSuffix)
	require.NoError(t, err)
	assert.JSONEq(t, savedEnvelope, string(saved), "a failed refresh must keep the previous result")

	refreshed := `{"status":"Succeeded","result":{"analyzerId":"prebuilt-image","contents":[]}}`
	f.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
		return cuclient.AnalyzeResponse{JSON: json.RawMessage(refreshed)}, nil
	}
	_, stderr, err := f.execute(t.Context(), input, "--refresh", "-o", "json")
	require.NoError(t, err)
	assert.Equal(t, savedResultNotice(input), stderr)
	assert.EqualValues(t, 3, f.client.calls.Load())
	// #nosec G304 -- Reads only this test's private temporary result.
	saved, err = os.ReadFile(input + analysis.ResultSuffix)
	require.NoError(t, err)
	assert.JSONEq(t, refreshed, string(saved))
}

func TestAnalyzeInvalidSavedResultsNeverCallService(t *testing.T) {
	for _, tt := range []struct{ name, saved string }{
		{"malformed", `{"result":`},
		{"not an analysis", `{"items":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, input := savedResultFixture(t, savedEnvelope)
			require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
			require.NoError(t, os.WriteFile(input+analysis.ResultSuffix, []byte(tt.saved), 0o600))
			stdout, _, err := f.execute(t.Context(), input)
			local := requireLocalCode(t, err, "invalid_saved_result")
			assert.Contains(t, local.Suggestion, "--refresh")
			assert.Empty(t, stdout)
			assert.Zero(t, f.client.calls.Load())
		})
	}
}

func TestAnalyzeUnmatchedSavedResultsAreKept(t *testing.T) {
	other := `{"status":"Succeeded","result":{"analyzerId":"prebuilt-layout","contents":[]}}`
	for _, tt := range []struct {
		name, saved, reason string
		stale               bool
	}{
		{"other analyzer", other, "was produced by a different analyzer", false},
		{"stale", savedEnvelope, "is older than the input file", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, input := savedResultFixture(t, savedEnvelope)
			require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
			require.NoError(t, os.WriteFile(input+analysis.ResultSuffix, []byte(tt.saved), 0o600))
			if tt.stale {
				later := time.Now().Add(time.Hour)
				require.NoError(t, os.Chtimes(input, later, later))
			}
			stdout, stderr, err := f.execute(t.Context(), input, "--pretty=false")
			require.NoError(t, err)
			assert.Contains(t, stdout, "Picture")
			assert.Equal(t, "The saved result at "+input+analysis.ResultSuffix+" "+tt.reason+
				"; analyzing without replacing it. Pass --refresh to replace it.\n", stderr)
			assert.EqualValues(t, 1, f.client.calls.Load())
			// #nosec G304 -- Reads only this test's private temporary result.
			saved, err := os.ReadFile(input + analysis.ResultSuffix)
			require.NoError(t, err)
			assert.Equal(t, tt.saved, string(saved), "an unmatched result is replaced only by --refresh")

			_, stderr, err = f.execute(t.Context(), input, "--refresh")
			require.NoError(t, err)
			assert.Equal(t, savedResultNotice(input), stderr)
			// #nosec G304 -- Reads only this test's private temporary result.
			saved, err = os.ReadFile(input + analysis.ResultSuffix)
			require.NoError(t, err)
			assert.JSONEq(t, savedEnvelope, string(saved))
		})
	}
}

func TestAnalyzeCacheDisabledNeitherReadsNorWrites(t *testing.T) {
	f, input := savedResultFixture(t, savedEnvelope)
	require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
	_, stderr, err := f.execute(t.Context(), input, "--cache=false")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.NoFileExists(t, input+analysis.ResultSuffix)

	require.NoError(t, os.WriteFile(input+analysis.ResultSuffix, []byte(`{"result":`), 0o600))
	_, _, err = f.execute(t.Context(), input, "--cache=false")
	require.NoError(t, err, "--cache=false must ignore an unusable saved result")
	assert.EqualValues(t, 2, f.client.calls.Load())
	// #nosec G304 -- Reads only this test's private temporary result.
	saved, err := os.ReadFile(input + analysis.ResultSuffix)
	require.NoError(t, err)
	assert.Equal(t, `{"result":`, string(saved))
}

func TestAnalyzeSavedResultScope(t *testing.T) {
	f, input := savedResultFixture(t, savedEnvelope)
	require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
	output := filepath.Join(testutil.TempDir(t), "out.json")
	for _, tt := range []struct {
		name string
		args []string
		code string
	}{
		{"refresh without cache", []string{input, "--refresh", "--cache=false"}, "conflicting_analyze_options"},
		{"refresh output file", []string{input, "--refresh", "--output-file", output}, "refresh_not_applicable"},
		{"refresh url", []string{"https://example.com/a.png", "--refresh"}, "refresh_not_applicable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := f.execute(t.Context(), tt.args...)
			requireLocalCode(t, err, tt.code)
			assert.Zero(t, f.opens)
		})
	}
	_, stderr, err := f.execute(t.Context(), input, "-o", "json", "--output-file", output)
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.NoFileExists(t, input+analysis.ResultSuffix, "explicit destinations keep --on-existing semantics")
	_, stderr, err = f.execute(t.Context(), "https://example.com/a.png")
	require.NoError(t, err)
	assert.Empty(t, stderr, "URL inputs are not persisted automatically")
}

func TestAnalyzeSaveFailureWarnsAndStillShowsResult(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory permissions enforced for a non-root user")
	}
	f, input := savedResultFixture(t, savedEnvelope)
	require.NoError(t, os.WriteFile(input, []byte("image"), 0o600))
	directory := filepath.Dir(input)
	require.NoError(t, os.Chmod(directory, 0o500))
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	stdout, stderr, err := f.execute(t.Context(), input, "--pretty=false")
	require.NoError(t, err)
	assert.Contains(t, stdout, "Picture")
	assert.Contains(t, stderr, "Warning: The complete analysis result could not be saved to "+input+analysis.ResultSuffix)
	assert.NotContains(t, stderr, "Saved complete analysis result")
	assert.NoFileExists(t, input+analysis.ResultSuffix)
}
