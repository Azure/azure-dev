// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// evaluatorServing answers the version listing with versions and any point read
// with document.
func evaluatorServing(t *testing.T, versions []string, document string) *evalContext {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/versions") {
			rows := make([]string, 0, len(versions))
			for _, v := range versions {
				rows = append(rows, `{"name":"quality","version":"`+v+`"}`)
			}
			_, _ = w.Write([]byte(`{"value":[` + strings.Join(rows, ",") + `]}`))
			return
		}
		_, _ = w.Write([]byte(document))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}
}

func evaluatorDownloadCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	cmd.SetContext(t.Context())
	return cmd
}

// The definition lands under a name derived from the evaluator and the version,
// so fetching several into one directory does not have them overwrite in turn.
func TestEvaluatorDownloadWritesTheDefinition(t *testing.T) {
	dir := t.TempDir()
	ec := evaluatorServing(t, []string{"3"}, `{"name":"quality","version":"3","kind":"rubric"}`)

	a := &evaluatorDownloadAction{cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outputDir: dir}
	require.NoError(t, a.download(t.Context(), ec))

	written := filepath.Join(dir, "quality-3.json")
	body, err := os.ReadFile(written)
	require.NoError(t, err, "expected the definition at %s", written)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Equal(t, "quality", doc["name"])
	require.Equal(t, "rubric", doc["kind"], "the service's document is written whole")
}

// Omitting --version resolves the latest, and the file says which it was --
// otherwise the only record of what was fetched is gone.
func TestEvaluatorDownloadResolvesTheLatestVersion(t *testing.T) {
	dir := t.TempDir()
	ec := evaluatorServing(t, []string{"1", "9", "15"}, `{"name":"quality","version":"15"}`)

	a := &evaluatorDownloadAction{cmd: evaluatorDownloadCmd(t), name: "quality", outputDir: dir}
	require.NoError(t, a.download(t.Context(), ec))

	require.FileExists(t, filepath.Join(dir, "quality-15.json"),
		"15 is the latest; 9 would mean the versions were compared as text")
}

// A download replaces nothing it was not told to. The path is derived, so it
// can collide with a file the caller wrote themselves.
func TestEvaluatorDownloadRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "quality-3.json")
	require.NoError(t, os.WriteFile(existing, []byte("mine\n"), 0o600))

	ec := evaluatorServing(t, []string{"3"}, `{"name":"quality","version":"3"}`)
	a := &evaluatorDownloadAction{cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outputDir: dir}

	require.Error(t, a.download(t.Context(), ec))

	body, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, "mine\n", string(body), "the caller's file has to survive a refusal")

	a.force = true
	require.NoError(t, a.download(t.Context(), ec))
	body, err = os.ReadFile(existing)
	require.NoError(t, err)
	require.NotEqual(t, "mine\n", string(body), "--force is what replaces it")
}

// Both flags name a destination, and honouring either silently writes somewhere
// the caller did not ask for.
func TestEvaluatorDownloadRefusesTwoDestinations(t *testing.T) {
	cmd := evaluatorDownloadCmd(t)
	a := &evaluatorDownloadAction{
		cmd: cmd, name: "quality", outputDir: t.TempDir(), outFile: filepath.Join(t.TempDir(), "q.json"),
	}
	require.Error(t, a.Run())
}

// --output-file writes exactly where it says.
func TestEvaluatorDownloadHonoursAnExactPath(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nested", "mine.json")
	ec := evaluatorServing(t, []string{"3"}, `{"name":"quality","version":"3"}`)

	a := &evaluatorDownloadAction{cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outFile: dest}
	require.NoError(t, a.download(t.Context(), ec))

	require.FileExists(t, dest)
}

// The name and version are interpolated into a path, so neither may be anything
// but one component -- `--version ../..` would otherwise resolve outside the
// output directory, since filepath.Join cleans `..` rather than refusing it.
func TestEvaluatorDownloadRefusesAVersionThatIsNotAPathComponent(t *testing.T) {
	dir := t.TempDir()
	ec := evaluatorServing(t, []string{"3"}, `{"name":"quality","version":"3"}`)

	a := &evaluatorDownloadAction{
		cmd: evaluatorDownloadCmd(t), name: "quality", version: "../../escaped", outputDir: dir,
	}
	require.Error(t, a.download(t.Context(), ec))
}
