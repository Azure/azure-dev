// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner is a test-only azdRunner. Output and Run return whatever
// the test pre-loaded.
type fakeRunner struct {
	outputBytes []byte
	outputErr   error

	runErr error
	// runCalls records (args, stdout-bytes-passed-through) so tests can
	// assert on dispatch shape.
	runCalls []runCall
}

type runCall struct {
	args   []string
	stdout string
	stderr string
}

func (f *fakeRunner) Output(_ context.Context, _ []string) ([]byte, error) {
	return f.outputBytes, f.outputErr
}

func (f *fakeRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) error {
	// Capture by writing canned content into the streams so callers that
	// stream output through still see something.
	if stdout != nil {
		_, _ = stdout.Write([]byte("child stdout\n"))
	}
	if stderr != nil {
		_, _ = stderr.Write([]byte(""))
	}
	f.runCalls = append(f.runCalls, runCall{args: args})
	return f.runErr
}

func TestLookupExtension_ReturnsInstalledTrueWhenVersionSet(t *testing.T) {
	runner := &fakeRunner{
		outputBytes: []byte(`[
			{"id":"azure.ai.docs","namespace":"ai.doc","installedVersion":"0.0.1-preview"},
			{"id":"azure.ai.agents","namespace":"ai.agent","installedVersion":"0.1.33-preview"}
		]`),
	}
	got, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.NoError(t, err)
	assert.True(t, got.Installed)
	assert.Equal(t, "ai.doc", got.Namespace)
}

func TestLookupExtension_ReturnsInstalledFalseWhenVersionEmpty(t *testing.T) {
	runner := &fakeRunner{
		outputBytes: []byte(`[
			{"id":"azure.ai.docs","namespace":"ai.doc","installedVersion":""}
		]`),
	}
	got, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.NoError(t, err)
	assert.False(t, got.Installed)
	assert.Equal(t, "ai.doc", got.Namespace)
}

func TestLookupExtension_ReturnsInstalledFalseWhenAbsentFromCatalog(t *testing.T) {
	runner := &fakeRunner{outputBytes: []byte(`[]`)}
	got, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.NoError(t, err)
	assert.False(t, got.Installed)
	assert.Empty(t, got.Namespace, "absent extensions have no known namespace")
}

func TestLookupExtension_IsCaseInsensitive(t *testing.T) {
	runner := &fakeRunner{
		outputBytes: []byte(`[{"id":"AZURE.AI.DOCS","namespace":"ai.doc","installedVersion":"1"}]`),
	}
	got, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.NoError(t, err)
	assert.True(t, got.Installed)
}

func TestLookupExtension_PropagatesListError(t *testing.T) {
	runner := &fakeRunner{outputErr: errors.New("network down")}
	_, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azd ext list")
}

func TestLookupExtension_PropagatesParseError(t *testing.T) {
	runner := &fakeRunner{outputBytes: []byte(`not json`)}
	_, err := lookupExtension(context.Background(), runner, "azure.ai.docs")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestInstallExtension_DispatchesExtInstall(t *testing.T) {
	runner := &fakeRunner{}
	var stdout, stderr bytes.Buffer

	err := installExtension(context.Background(), runner, "azure.ai.docs", &stdout, &stderr)
	require.NoError(t, err)

	require.Len(t, runner.runCalls, 1)
	assert.Equal(t, []string{"ext", "install", "azure.ai.docs"}, runner.runCalls[0].args)
	assert.Equal(t, "child stdout\n", stdout.String(), "child output must stream through")
}

func TestInstallExtension_PropagatesError(t *testing.T) {
	runner := &fakeRunner{runErr: errors.New("install failed")}
	err := installExtension(context.Background(), runner, "azure.ai.docs", io.Discard, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "install failed")
}

func TestRunChildAzd_PassesArgsVerbatim(t *testing.T) {
	runner := &fakeRunner{}
	args := []string{"ai", "doc", "skills", "install", "--target", "copilot", "--no-prompt"}
	require.NoError(t, runChildAzd(context.Background(), runner, args, io.Discard, io.Discard))
	require.Len(t, runner.runCalls, 1)
	assert.Equal(t, args, runner.runCalls[0].args)
}
