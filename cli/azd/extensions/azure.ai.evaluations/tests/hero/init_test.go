// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build hero

// Package hero drives the hero scenarios through real azd, with the extension
// installed the way a user installs it.
//
// The CLI suite in ../cli runs the extension binary directly, which covers the
// command surface but cannot reach `init`: `init` resolves the project and
// edits azure.yaml over azd's gRPC channel, so without azd hosting the process
// there is nothing on the other end. That is not a detail — it is the first
// command in Scenario 1 and the one that produces the local diff every later
// step depends on, and until now the only thing asserting its output was a
// unit test calling the scaffold function directly. A unit test cannot see the
// service entry azd writes, the detection that reads the project, or the
// terminal output the spec pins line for line.
//
//	azd x pack --rebuild
//	azd extension install azure.ai.evaluations --source local
//	go test -tags hero -v ./tests/hero/...
package hero

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// reinstall is what to run when the installed extension is not this code.
//
// `azd x pack` rewrites the artifacts but leaves the checksum in the local
// registry alone when the version has not changed, so a plain reinstall then
// fails validation. Bumping the version in extension.yaml is the way through.
const reinstall = "  azd x pack --rebuild\n" +
	"  azd extension uninstall azure.ai.evaluations\n" +
	"  azd extension install azure.ai.evaluations --source local\n"

// TestMain refuses to run against an azd that cannot reach the extension, or
// that is hosting a different build of it.
//
// Skipping would be worse than failing here: these tests exist because nothing
// else covers the azd-hosted path, so a silent skip returns the suite to the
// state it was in before they were written. Running against a stale install is
// worse still — it reports on code that is not the code under test, which is
// the one outcome a test must never produce.
func TestMain(m *testing.M) {
	if os.Getenv("AZURE_AI_EVAL_HERO") != "1" {
		fmt.Fprintf(os.Stderr,
			"set AZURE_AI_EVAL_HERO=1 to run the hero scenarios. They need azd "+
				"hosting this extension:\n%s", reinstall)
		os.Exit(0)
	}

	hosted, err := exec.Command("azd", "ai", "eval", "init", "--help").CombinedOutput()
	if err != nil || !strings.Contains(string(hosted), "Scaffold evaluation config") {
		fmt.Fprintf(os.Stderr,
			"azd cannot reach the evaluations extension. Install it first:\n%s\n%s\n",
			reinstall, hosted)
		os.Exit(1)
	}

	if err := requireCurrentInstall(string(hosted)); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s", err, reinstall)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// requireCurrentInstall compares the installed extension's help against this
// working tree's, so a stale install fails loudly instead of quietly reporting
// on the wrong binary.
//
// Help text is the cheapest available fingerprint that actually moves: it
// carries every command and flag, which is what these tests assert on, and it
// costs one build rather than a version stamp nobody remembers to bump.
func requireCurrentInstall(hosted string) error {
	dir, err := os.MkdirTemp("", "azdeval-hero")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	binary := filepath.Join(dir, "azdeval"+exeSuffix())
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building this working tree to compare against: %v\n%s", err, out)
	}

	local, err := exec.Command(binary, "init", "--help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reading this working tree's help: %w", err)
	}

	if normalize(string(local)) != normalize(hosted) {
		return fmt.Errorf(
			"azd is hosting a different build of this extension.\n"+
				"installed:\n%s\nthis working tree:\n%s",
			normalize(hosted), normalize(string(local)))
	}
	return nil
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

// project writes a minimal azd project for `init` to attach to.
//
// It declares the two services detection reads — the Foundry project and the
// agent — because what `init` writes into azure.yaml depends on which of them
// exist, and a project with neither would exercise only the fallback.
func project(t *testing.T, agent string) string {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`name: support-app
services:
  ai-project:
    host: azure.ai.project
  %s:
    host: azure.ai.agent
`, agent)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(body), 0o600))
	return dir
}

// azdEval runs the extension through azd, in dir.
func azdEval(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command("azd", append([]string{"ai", "eval"}, args...)...)
	cmd.Dir = dir
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("could not run azd ai eval %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}

	// azd prints its own upgrade notice to stderr, which is not the command's
	// output and would break an exact comparison.
	text := dropUpgradeNotice(out.String())
	t.Logf("$ azd ai eval %s -> exit %d\n%s", strings.Join(args, " "), code, text)
	return text, code
}

// dropUpgradeNotice removes azd's "Update available" banner and everything
// after it, which azd appends regardless of the command.
func dropUpgradeNotice(s string) string {
	if i := strings.Index(s, "Update available:"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " \r\n\t")
}

// normalize makes terminal output comparable across platforms.
func normalize(s string) string {
	return strings.ReplaceAll(dropUpgradeNotice(s), "\r\n", "\n")
}

// TestHeroScenario1ColdStart is the first half of Scenario 1: the offline
// baseline `init` writes, asserted against the terminal block the spec shows.
//
// The output is compared whole rather than by keyword. Every line of it is a
// promise the spec makes to a reader deciding whether to adopt this — which
// files appear, what was detected, what to run next — and a keyword assertion
// would pass while the reader's terminal said something else.
func TestHeroScenario1ColdStart(t *testing.T) {
	const (
		agent = "support-agent"
		judge = "gpt-5.6-luna"
	)
	dir := project(t, agent)

	// The evaluator and judge are passed rather than prompted for, because the
	// spec's two `?` lines are answers to prompts and a test has no terminal to
	// answer them at.
	out, code := azdEval(t, dir, "init",
		"--target", agent, "--source", "traces",
		"--evaluator", "builtin.task_adherence", "--judge-model", judge)
	require.Zero(t, code, "init makes no service calls, so nothing can fail it here")

	want := `(✓) Done: Detected agent target: support-agent
(✓) Done: Using data source: traces (Application Insights)
(✓) Done: Judge model deployment: gpt-5.6-luna

Created
  evals/azure.eval.yaml             evaluation configuration
  azure.yaml                        added service 'support-agent-evals'

Next: azd up
      azd ai eval run start`

	require.Equal(t, want, normalize(out))
}

// Scenario 1's second half: the azure.eval.yaml the terminal block promised. The spec
// prints this file, so its shape is as much a promise as the output above —
// and it is the file a reader reviews before running `azd up`.
func TestHeroScenario1WritesTheDocumentedConfig(t *testing.T) {
	dir := project(t, "support-agent")

	_, code := azdEval(t, dir, "init",
		"--target", "support-agent", "--source", "traces",
		"--evaluator", "builtin.task_adherence", "--judge-model", "gpt-5.6-luna")
	require.Zero(t, code)

	body, err := os.ReadFile(filepath.Join(dir, "evals", "azure.eval.yaml"))
	require.NoError(t, err)
	text := string(body)

	require.Contains(t, text, "name: support-agent-trace-eval")
	require.Contains(t, text, "type: traces")
	require.Contains(t, text, "agent_name: support-agent",
		"a trace run has no target, so agent_name is what scopes it")
	require.Contains(t, text, "max_traces: 20",
		"a first run is bounded rather than taking the service default of 1000")
	require.Contains(t, text, "evaluator: builtin.task_adherence")
	require.Contains(t, text, "model: gpt-5.6-luna",
		"the judge is written per evaluator reference as initialization_parameters.model")

	require.NotContains(t, text, "datasets:",
		"there is no file to register, so the catalog is absent rather than empty")
	require.NotContains(t, text, "target:",
		"a trace run invokes nothing")
}

// `init` is offline, and being offline is the property that makes its output a
// reviewable local diff. A service call here would also make the command fail
// for a user who has not authenticated yet, which is exactly when they run it.
func TestHeroInitMakesNoServiceCalls(t *testing.T) {
	dir := project(t, "support-agent")

	cmd := exec.Command("azd", "ai", "eval", "init",
		"--target", "support-agent", "--evaluator", "builtin.task_adherence",
		"--judge-model", "m")
	cmd.Dir = dir
	// A proxy pointing nowhere fails any outbound request, so a command that
	// stays offline is unaffected and one that does not cannot be mistaken for
	// working.
	cmd.Env = append(os.Environ(),
		"HTTPS_PROXY=http://127.0.0.1:9",
		"HTTP_PROXY=http://127.0.0.1:9",
		"NO_PROXY=",
	)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "init must not need the network:\n%s", out)
}

// The eval service has to be declared in azure.yaml before azd will act on it.
// Printing the block and leaving the edit to the reader was enough, once, to
// make the documented flow stop working between `init` and `azd up`.
func TestHeroInitWiresTheServiceIntoTheProject(t *testing.T) {
	dir := project(t, "support-agent")

	_, code := azdEval(t, dir, "init", "--target", "support-agent",
		"--evaluator", "builtin.task_adherence", "--judge-model", "m")
	require.Zero(t, code)

	root, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	text := string(root)

	require.Contains(t, text, "support-agent-evals:",
		"the service is named for the agent it evaluates")
	require.Contains(t, text, "host: azure.ai.eval")
	require.Contains(t, text, "$ref: ./evals/azure.eval.yaml")

	// azd owns the edit, so everything the project already declared survives it.
	require.Contains(t, text, "name: support-app")
	require.Contains(t, text, "host: azure.ai.project")
	require.Contains(t, text, "host: azure.ai.agent")

	// The eval reads both, so azd has to deploy both first.
	require.Regexp(t, `(?s)support-agent-evals:.*uses:.*ai-project.*support-agent`, text)
}

// Running `init` twice must not deploy the same eval twice. The service key is
// the eval's name, so the second run recognizes its own work.
func TestHeroInitIsIdempotent(t *testing.T) {
	dir := project(t, "support-agent")
	args := []string{"init", "--target", "support-agent",
		"--evaluator", "builtin.task_adherence", "--judge-model", "m"}

	_, code := azdEval(t, dir, args...)
	require.Zero(t, code)
	first, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)

	out, code := azdEval(t, dir, args...)
	require.NotZero(t, code, "the scaffold already exists, so a second run must refuse")
	require.Contains(t, out, "--force", "the refusal has to say how to proceed")

	second, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(first), string(second),
		"a refused init must not have edited the project")

	// With --force the files are rewritten, and the service is still declared
	// exactly once.
	out, code = azdEval(t, dir, append(args, "--force")...)
	require.Zero(t, code, out)

	third, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(third), "host: azure.ai.eval"),
		"a second eval service would deploy the same eval twice")
	require.Contains(t, normalize(out), "already declares service 'support-agent-evals'")
}

// Evals attach to a project; they do not create one. Naming the command that
// makes a project is more use than a transport error from the gRPC channel
// that was not there.
func TestHeroInitNeedsAnAzdProject(t *testing.T) {
	dir := t.TempDir()

	out, code := azdEval(t, dir, "init", "--target", "support-agent", "--no-prompt")
	require.NotZero(t, code)
	require.Contains(t, out, "azd init")
	require.NotContains(t, strings.ToLower(out), "grpc",
		"a missing project must not surface as a transport error")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a refused init must leave nothing behind")
}

// Passing --evaluator replaces the defaults, which is how a caller opts out of
// rubric generation — so the "next" steps must stop offering to generate one.
func TestHeroInitExplicitEvaluatorsOptOutOfGeneration(t *testing.T) {
	dir := project(t, "support-agent")

	out, code := azdEval(t, dir, "init",
		"--target", "support-agent", "--judge-model", "m",
		"--evaluator", "builtin.task_adherence")
	require.Zero(t, code, out)

	text := normalize(out)
	require.NotContains(t, text, "evaluator generate",
		"nothing was scheduled to be generated, so nothing should be suggested")

	body, err := os.ReadFile(filepath.Join(dir, "evals", "azure.eval.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(body), "evaluator: builtin.task_adherence")
	require.NotContains(t, string(body), "support-agent-quality",
		"the default rubric was replaced, not added to")
}

// A supplied dataset is not generated either, so `init` has nothing left to
// suggest and must not send the reader to a command that would submit a job
// for an artifact they already have.
func TestHeroInitSuppliedDatasetIsNotGenerated(t *testing.T) {
	dir := project(t, "support-agent")

	out, code := azdEval(t, dir, "init",
		"--target", "support-agent", "--judge-model", "m",
		"--dataset", "prod-golden",
		"--evaluator", "builtin.task_adherence")
	require.Zero(t, code, out)

	text := normalize(out)
	require.NotContains(t, text, "dataset generate")
	require.Contains(t, text, "Next: azd up",
		"with nothing left to generate, the next step is the deploy")

	body, err := os.ReadFile(filepath.Join(dir, "evals", "azure.eval.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(body), "dataset: prod-golden")
	require.NotContains(t, string(body), "source:",
		"a registered dataset has nothing to upload")
}
