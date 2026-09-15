// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two services pointing at one configuration is a project somebody hand-edited,
// but the answer still has to be the same one every run: GetServices is a map,
// and picking whichever came out first would make `init` report a different
// service each time it was asked.
func TestTwoServicesOnOneConfigurationResolveToTheSameOneEveryTime(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "evals", "azure.eval.yaml")
	proj := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"zeta-evals":  evalServiceNamed(t, "zeta-evals", "./evals/azure.eval.yaml"),
			"alpha-evals": evalServiceNamed(t, "alpha-evals", "./evals/azure.eval.yaml"),
			"mid-evals":   evalServiceNamed(t, "mid-evals", "./evals/azure.eval.yaml"),
		},
	}

	for range 20 {
		_, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)
		require.NoError(t, err)
		assert.Equal(t, "alpha-evals", name, "the pick has to be stable across map orderings")
	}
}

// The derived name being taken by a service pointing somewhere else is not a
// conflict when the configuration itself is already wired under another name:
// nothing is written, so there is nothing to refuse.
func TestADifferentServiceUnderTheDerivedNameIsNotAConflictWhenTheConfigIsWired(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "evals", "azure.eval.yaml")
	proj := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"agent-evals": evalServiceNamed(t, "agent-evals", "./somewhere/else.yaml"),
			"owner-evals": evalServiceNamed(t, "owner-evals", "./evals/azure.eval.yaml"),
		},
	}

	action, name, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.NoError(t, err, "the configuration is wired; init writes nothing and refuses nothing")
	assert.Equal(t, wiringPresent, action)
	assert.Equal(t, "owner-evals", name, "the service that owns this configuration is the one reported")
}

// With the configuration not wired anywhere, the derived name pointing
// elsewhere is still the conflict it was: init would have to overwrite it.
func TestTheDerivedNamePointingElsewhereIsStillRefused(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "evals", "azure.eval.yaml")
	proj := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"agent-evals": evalServiceNamed(t, "agent-evals", "./somewhere/else.yaml"),
		},
	}

	_, _, err := rootEvalServiceAction(proj, "agent-evals", configPath)

	require.Error(t, err, "overwriting a service that points at another file needs saying")
}

// jsonTree is a root carrying the inherited -o/--output flag with one failing
// command under it.
func jsonTree(t *testing.T, run func(*cobra.Command, []string) error) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	root := &cobra.Command{Use: "eval"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.AddCommand(&cobra.Command{Use: "thing", RunE: run})
	reportFailuresAsJSON(root)

	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	return root, out
}

// Only the caller who asked for JSON gets a document. A reader who asked for
// anything else is reading azd's line, and a JSON blob would be noise.
func TestOnlyTheJSONCallerGetsADocument(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"", "table", "none"} {
		t.Run("format="+format, func(t *testing.T) {
			root, out := jsonTree(t, func(*cobra.Command, []string) error {
				return errors.New("boom")
			})
			args := []string{"thing"}
			if format != "" {
				args = append(args, "-o", format)
			}
			root.SetArgs(args)
			require.Error(t, root.Execute())
			assert.Empty(t, out.String(), "only -o json answers with a document")
		})
	}
}

// A command that writes nothing and fails is the ordinary case, and it is the
// one that used to leave a parser reading an empty stream.
func TestAFailureThatWroteNothingStillAnswers(t *testing.T) {
	t.Parallel()

	root, out := jsonTree(t, func(*cobra.Command, []string) error {
		return errors.New("the deployment was not found")
	})
	root.SetArgs([]string{"thing", "-o", "json"})
	require.Error(t, root.Execute())

	var doc jsonError
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "stdout: %q", out.String())
	assert.Equal(t, "the deployment was not found", doc.Error.Message)
}

// Writing prose and then failing is not "already answered": under -o json the
// document is the only thing that reaches stdout, so anything written there is
// one. This pins that the recorder measures stdout rather than intent.
func TestAWriterThatWroteNothingIsNotTreatedAsHavingAnswered(t *testing.T) {
	t.Parallel()

	root, out := jsonTree(t, func(c *cobra.Command, _ []string) error {
		// A zero-length write must not count as an answer.
		_, _ = c.OutOrStdout().Write(nil)
		return errors.New("boom")
	})
	root.SetArgs([]string{"thing", "-o", "json"})
	require.Error(t, root.Execute())

	var doc jsonError
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "stdout: %q", out.String())
	assert.Equal(t, "boom", doc.Error.Message)
}

// The wrapper swaps the command's writer to watch it. Running twice must not
// stack one watcher on the next, and the second run has to behave like the
// first.
func TestRunningTwiceDoesNotStackWriters(t *testing.T) {
	t.Parallel()

	root, out := jsonTree(t, func(*cobra.Command, []string) error {
		return errors.New("boom")
	})

	for range 3 {
		out.Reset()
		root.SetArgs([]string{"thing", "-o", "json"})
		require.Error(t, root.Execute())

		var doc jsonError
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "stdout: %q", out.String())
		assert.Equal(t, "boom", doc.Error.Message)
	}
}

// An unquoted shell variable holding a name with spaces arrives as several
// arguments. That fails argument validation, which runs instead of RunE rather
// than before it, so it reached a script as an empty stream and no reason.
func TestTooManyArgumentsStillAnswersTheJSONCaller(t *testing.T) {
	t.Parallel()

	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"dataset", "create", "not", "a", "valid", "name", "-o", "json"})
	require.Error(t, root.Execute())

	var doc jsonError
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout under -o json has to parse as JSON: %q", out.String())
	assert.NotEmpty(t, doc.Error.Message, "the document has to say what was wrong")
}

// The same mistake without -o json is unchanged: azd prints the line.
func TestTooManyArgumentsWritesNothingToStdoutForAHuman(t *testing.T) {
	t.Parallel()

	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"dataset", "create", "not", "a", "valid", "name"})
	require.Error(t, root.Execute())
	assert.Empty(t, out.String())
}

// A `--path` that is already where every command looks needs no flag, whichever
// way the caller spelled it.
func TestTheDefaultDirectoryIsRecognisedHoweverItIsSpelled(t *testing.T) {
	here := t.TempDir()
	t.Chdir(here)
	here = mustGetwd(t)

	for _, spelling := range []string{
		project.DefaultEvalDir,
		"./" + project.DefaultEvalDir,
		filepath.Join(here, project.DefaultEvalDir),
	} {
		s := scaffold{eval: &project.Eval{Name: "an-eval"}, evalDir: spelling}
		steps := s.nextSteps()
		require.Len(t, steps, 1)
		assert.NotContains(t, steps[0], "--path", "spelled %q", spelling)
	}
}
