// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package e2elive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// liveEnvVar gates the live test: it only runs when set to "1". This keeps the
// expensive, Azure-touching test out of the normal `go test ./...` run.
const liveEnvVar = "AZURE_AI_AGENTS_E2E_LIVE"

// Virtual terminal dimensions for the interactive init phase.
const (
	initCols = 200
	initRows = 50
)

// Phase time budgets. The per-mode runTimeout must exceed the sum of the phase
// budgets so a slow-but-healthy run is never preempted (which would also skip
// the teardown and leak resources). Two modes at runTimeout each fit inside the
// `go test -timeout 125m` cap used by the pipeline, whose ADO step adds a small
// margin on top before force-killing the process.
const (
	runTimeout       = 60 * time.Minute
	initTimeout      = 8 * time.Minute
	provisionTimeout = 10 * time.Minute
	deployTimeout    = 10 * time.Minute
	invokeTimeout    = 3 * time.Minute
	monitorTimeout   = 60 * time.Second
	teardownTimeout  = 10 * time.Minute

	// Event-driven tuning for the interactive init loop. promptQuiet is how long
	// the survey UI must stop emitting before we treat the current prompt as
	// "drawn and waiting for input"; listSettle is the shorter pause we let a
	// filtered Select list redraw after typing before confirming with Enter.
	// Both replace the old fixed 3s poll; the hard init cap is the ctx deadline.
	promptQuiet = 800 * time.Millisecond
	listSettle  = 600 * time.Millisecond
)

// TestTier2Live exercises the full golden path against live Azure for each
// requested deploy mode, sequentially (concurrent deploys in one subscription
// race on shared resources and exhaust model quota).
func TestTier2Live(t *testing.T) {
	if os.Getenv(liveEnvVar) != "1" {
		t.Skipf("set %s=1 to run the live Tier 2 golden-path test", liveEnvVar)
	}

	for _, mode := range deployModesFromEnv() {
		t.Run(mode, func(t *testing.T) {
			r := newRunner(t, mode)
			ctx, cancel := context.WithTimeout(t.Context(), runTimeout)
			defer cancel()
			r.run(ctx)
		})
	}
}

// deployModesFromEnv reads E2E_DEPLOY_MODES (code|container|both); default both.
func deployModesFromEnv() []string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("E2E_DEPLOY_MODES"))) {
	case "code":
		return []string{"code"}
	case "container":
		return []string{"container"}
	default:
		return []string{"code", "container"}
	}
}

// runner holds the per-mode state for one golden-path run.
type runner struct {
	t          *testing.T
	mode       string
	testDir    string
	agentName  string
	env        []string
	projectDir string
	c          *console
}

// newRunner prepares an isolated working directory, a private AZD_CONFIG_DIR
// (copied from ~/.azd so the installed extension is available), and a unique
// agent name, then registers teardown so resources are cleaned up even on
// failure.
func newRunner(t *testing.T, mode string) *runner {
	t.Helper()

	testDir := getenvDefault("E2E_TESTDIR", "/tmp/e2e-tests/tier2-"+mode)
	if err := assertSafeTestDir(testDir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(testDir); err != nil {
		t.Fatalf("clean test dir: %v", err)
	}
	if err := os.MkdirAll(testDir, 0o700); err != nil {
		t.Fatalf("create test dir: %v", err)
	}

	configDir := filepath.Join(os.TempDir(), "e2e-azd-config-"+mode)
	setupConfigDir(t, configDir)

	env := os.Environ()
	env = append(env, "AZD_CONFIG_DIR="+configDir)
	if tenant := os.Getenv("E2E_TENANT"); tenant != "" {
		env = append(env, "AZURE_TENANT_ID="+tenant)
	}
	if tok := ghToken(); tok != "" {
		env = append(env, "GH_TOKEN="+tok, "GITHUB_TOKEN="+tok)
	}

	r := &runner{
		t:         t,
		mode:      mode,
		testDir:   testDir,
		agentName: fmt.Sprintf("e2e-%s-%s", mode, shortHash(mode)),
		env:       env,
	}

	// Cleanups run LIFO, so register the config-dir delete first and teardown
	// second: teardown (azd down) runs before the config copy it relies on is
	// removed.
	if !envTrue("E2E_KEEP_ARTIFACTS") {
		t.Cleanup(func() { _ = os.RemoveAll(configDir) })
	}
	t.Cleanup(r.teardown)

	// CI (GitHub Actions / Azure DevOps / explicit override) uses the az CLI
	// session for auth; local WSL uses azd's slower-to-avoid built-in auth.
	if useAzCliAuth() {
		_, _ = r.runAzd(t.Context(), testDir, time.Minute,
			"config", "set", "auth.useAzCliAuth", "true")
	}

	return r
}

// setupConfigDir creates configDir as a copy of ~/.azd (so installed extensions
// resolve), or an empty dir if ~/.azd is absent. cp -a preserves the extension
// binary's executable bit.
func setupConfigDir(t *testing.T, configDir string) {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}
	defaultAzd := filepath.Join(home, ".azd")
	if info, err := os.Stat(defaultAzd); err == nil && info.IsDir() {
		_ = os.RemoveAll(configDir)
		//nolint:gosec // both paths derive from HOME / TempDir, not user input.
		out, err := exec.Command("cp", "-a", defaultAzd, configDir).CombinedOutput()
		if err != nil {
			t.Fatalf("copy azd config dir: %v: %s", err, out)
		}
		return
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("create azd config dir: %v", err)
	}
}

// run executes the phases in order, stopping at the first failure. Teardown is
// registered separately as a cleanup, so it always runs.
func (r *runner) run(ctx context.Context) {
	if err := r.phaseInit(ctx); err != nil {
		r.t.Errorf("init: %v", err)
		return
	}
	if err := r.phaseProvision(ctx); err != nil {
		r.t.Errorf("provision: %v", err)
		return
	}
	if err := r.phaseDeploy(ctx); err != nil {
		r.t.Errorf("deploy: %v", err)
		return
	}
	if err := r.phaseInvoke(ctx); err != nil {
		r.t.Errorf("invoke: %v", err)
		return
	}
}

// phaseInit runs `azd ai agent init` attached to a pseudo-terminal and drives
// its interactive prompts until the project is scaffolded on disk.
func (r *runner) phaseInit(ctx context.Context) error {
	c, err := newConsole(initCols, initRows)
	if err != nil {
		return err
	}
	defer c.close()
	r.c = c

	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	// Deploy mode is NOT an interactive prompt in the template/--agent-name
	// flow: init auto-resolves it to "container" when a manifest is provided
	// (init_from_code.go:1373), so it must be chosen via the --deploy-mode flag
	// (init.go:1306). r.mode is exactly "container" or "code".
	args := []string{"ai", "agent", "init", "--agent-name", r.agentName, "--deploy-mode", r.mode}
	//nolint:gosec // azd is a trusted fixed binary; args are test-controlled.
	cmd := exec.CommandContext(ictx, "azd", args...)
	cmd.Dir = r.testDir
	cmd.Env = r.env
	cmd.Stdin = c.tty()
	cmd.Stdout = c.tty()
	cmd.Stderr = c.tty()
	// Give the child the pts as its controlling terminal (as tmux did), so
	// survey treats it as a real interactive terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start azd ai agent init: %w", err)
	}

	// No separate render goroutine: go-expect's passthrough pipe drains the
	// child's pty in the background, and driveInit's expect()/waitForQuiet()
	// calls do the reading that renders the screen. (A concurrent reader would
	// race those calls for the same stream.)
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	driveErr := r.driveInit(ictx, exited)

	// Make sure the child is gone before returning (it normally exits itself).
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}

	return driveErr
}

// driveInit is the event-driven prompt loop: it waits (via go-expect) for the
// survey UI to settle on a prompt, reads the rendered screen, and answers it,
// until init reports completion (or the process exits, or it times out).
//
// Why a screen-dispatch loop and not a linear ExpectString script: the live
// model/deployment and Foundry-project sub-flows branch on runtime state —
// whether the just-created project already has the model deployed, region/model
// availability, existing-name collisions — so the exact set and order of
// prompts cannot be predetermined. A linear ExpectString sequence would desync
// at the first conditional prompt. Instead we block on output settling (the
// go-expect read), then dispatch on the verbatim prompt strings the extension
// prints (each case annotated with its source file:line).
func (r *runner) driveInit(ctx context.Context, exited <-chan struct{}) error {
	var lastKey string
	repeat := 0

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("init timed out: %w\n--- tail ---\n%s",
				ctx.Err(), tail(r.c.tailString(), 2000))
		case <-exited:
			return r.finishInit(ctx)
		default:
		}

		// Block until the UI stops emitting (prompt fully drawn, awaiting input)
		// or the child exits. Replaces the old fixed-interval poll.
		if r.c.waitForQuiet(promptQuiet) {
			return r.finishInit(ctx)
		}

		screen := r.c.screen()
		if isInitComplete(screen) {
			return r.finishInit(ctx)
		}

		prompt := activePrompt(screen)
		if prompt == "" {
			continue // spinner / transient output, not a survey prompt yet
		}
		r.t.Logf("prompt: %s", truncate(prompt, 100))

		// Loop detection: compare the question text before ':' so varying filter
		// text on the same prompt doesn't reset the counter.
		key := promptKey(prompt)
		if key == lastKey {
			repeat++
		} else {
			repeat, lastKey = 1, key
		}
		if repeat >= 3 {
			if strings.Contains(prompt, "model") || strings.Contains(prompt, "is specified") {
				r.t.Log("loop detected on model prompt; trying next option")
				r.c.send(keyDown)
				r.c.waitForQuiet(listSettle)
				r.c.send(keyEnter)
				continue
			}
			if repeat >= 5 {
				return fmt.Errorf("init stuck in prompt loop: %q\n--- screen ---\n%s", key, screen)
			}
		}

		r.dispatchPrompt(screen, prompt)
	}
}

// finishInit confirms init produced the expected artifacts on disk, allowing a
// brief grace for files to flush after the completion marker or process exit.
func (r *runner) finishInit(ctx context.Context) error {
	if r.validateInitOutput() {
		return nil
	}
	_ = sleepCtx(ctx, 5*time.Second)
	if r.validateInitOutput() {
		return nil
	}
	return fmt.Errorf(
		"init finished but expected artifacts are missing on disk\n--- tail ---\n%s",
		tail(r.c.tailString(), 2000),
	)
}

// isInitComplete reports whether the success marker is on screen. Source:
// init.go:1483 prints "AI agent definition added to your azd project
// successfully!" in green at the end of runInitFromManifest.
func isInitComplete(screen string) bool {
	return screenContains(screen, "added to your azd project") ||
		screenContains(screen, "agent definition added")
}

// promptKey reduces a prompt line to its stable question text (before the first
// ':') for loop detection.
func promptKey(prompt string) string {
	if i := strings.Index(prompt, ":"); i > 0 {
		return strings.TrimSpace(prompt[:i])
	}
	return prompt
}

// dispatchPrompt answers a single survey prompt. Cases are ordered specific →
// generic and keyed on the verbatim messages the extension prints; the file:line
// in each comment points at the source string this matches. The prompt argument
// is already lowercased (see activePrompt).
//
// Only a subset of these fire on the --agent-name template critical path
// (language, template, Foundry project, subscription, location, the manifest
// model, deployment name, capacity/sku/version). The rest are kept as defensive
// handlers because init auto-resolves them under userProvidedManifest=true (so
// they normally do NOT prompt) or only surfaces them for specific runtime state.
func (r *runner) dispatchPrompt(screen, prompt string) {
	has := func(sub string) bool { return strings.Contains(prompt, sub) }

	switch {
	// Yes/No confirms. "Continue with this existing agent name?" (init.go:722)
	// only fires when the unique name already exists; decline it to reach the
	// fresh-name input. Any other confirm: accept.
	case has("[y/n]") || has("(y/n)") || has("continue with this existing agent name"):
		if has("continue with this existing agent name") {
			r.c.send("n")
		} else {
			r.c.send("y")
		}
		r.c.send(keyEnter)

	// Language select — "Select a language" (init_from_templates_helpers.go:263).
	case has("select a language"):
		r.selectByText("Python")

	// Template select — "Select a starter template" / "Select an agent template"
	// (init_from_templates_helpers.go:304 / 327).
	case has("starter template") || has("agent template"):
		r.selectByText("Basic agent (Invocations")

	// Foundry project hosting — "Select a Foundry project to host your agent..."
	// (init.go:1752 / 1910); choices "Use an existing..." / "Create a new...".
	case has("foundry project to host"):
		if r.createProject() {
			r.selectByText("Create a new Foundry project")
		} else {
			r.selectByText("Use an existing Foundry project")
		}

	// Existing-project picker — "Select a Foundry project"
	// (init_foundry_resources_helpers.go:1360); only when reusing a project.
	case has("select a foundry project"):
		if p := os.Getenv("E2E_PROJECT"); p != "" {
			r.selectByText(p)
		} else {
			r.enter()
		}

	// Subscription — preamble "Select an Azure subscription..."
	// (init_foundry_resources_helpers.go:905) + azd-core picker.
	case has("subscription"):
		if sub := os.Getenv("E2E_SUBSCRIPTION"); sub != "" {
			r.selectByText(sub[:min(8, len(sub))])
		} else {
			r.enter()
		}

	// Location — preamble "Select an Azure location..."
	// (init_foundry_resources_helpers.go:1004) + azd-core picker.
	case has("location") || has("region"):
		r.selectByText(getenvDefault("E2E_LOCATION", "eastus2"))

	// Manifest model decision — "Model '%s' is specified in the agent manifest."
	// (init_models.go:463); keep the manifest model (default first choice).
	case has("is specified in the agent manifest"):
		r.enter()

	// Existing deployments / generic proceed — init_models.go:263 / 330.
	case has("how would you like to proceed") || has("existing deployment"):
		r.enter()

	// Model deployment name input — init_models.go:398 (default = model name).
	case has("model deployment name") || (has("deployment name") && has("model")):
		r.enter()

	// Model select — "Select a model" (init_models.go:704 etc.).
	case has("select a model"):
		r.selectByText("gpt-4o-mini")

	// Deployment capacity / sku / version — azd-core PromptAiDeployment
	// (init_models.go:519); accept defaults.
	case has("capacity") || has("sku") || has("version"):
		r.enter()

	// Code-deploy prompts (init_from_code.go:1508 / 1534 / 1563). Auto-resolved
	// under userProvidedManifest=true, so kept as defensive handlers only.
	case has("select the runtime for your agent"):
		r.enter() // default Python 3.13
	case has("entry point"):
		r.enter() // accept detected default
	case has("how should dependencies be resolved"):
		r.enter() // default remote build

	// Optional infra (blank => create new): ACR login server
	// (init_foundry_resources_helpers.go:481), App Insights (:606 / :621).
	case has("acr login server") || has("container registry"):
		r.enter()
	case has("application insights"):
		r.enter()

	// Startup command (helpers.go:773); blank => skip.
	case has("command to start your agent"):
		r.enter()

	// Replacement agent name after declining the existing-name confirm
	// (init.go:745) / the name input (init.go:261); accept the default.
	case has("enter a different name for your agent") || has("enter a name for your agent"):
		r.enter()

	default:
		// No specific case matched: send Enter as a safe default, but log the
		// fall-through so CI can distinguish "matched and answered correctly"
		// from "hit the catch-all" when a new or changed prompt appears.
		r.t.Logf("unhandled prompt (default Enter): %s", truncate(prompt, 100))
		r.enter()
	}
}

// phaseProvision finds the scaffolded project and runs `azd provision`.
func (r *runner) phaseProvision(ctx context.Context) error {
	dir := r.findProjectDir()
	if dir == "" {
		return errors.New("no project directory with azure.yaml found")
	}
	r.projectDir = dir
	r.t.Logf("project dir: %s", dir)

	_, code := r.runAzd(ctx, dir, provisionTimeout, "provision", "--no-prompt")
	if code != 0 {
		return fmt.Errorf("azd provision failed (exit %d)", code)
	}
	return nil
}

// phaseDeploy runs `azd deploy`.
func (r *runner) phaseDeploy(ctx context.Context) error {
	_, code := r.runAzd(ctx, r.projectDir, deployTimeout, "deploy", "--no-prompt")
	if code != 0 {
		return fmt.Errorf("azd deploy failed (exit %d)", code)
	}
	return nil
}

// phaseInvoke calls the deployed agent and verifies it answers "2+2" with 4.
func (r *runner) phaseInvoke(ctx context.Context) error {
	wait := 30 * time.Second
	if r.mode == "container" {
		wait = 60 * time.Second
	}
	r.t.Logf("waiting %s for agent startup (%s mode)", wait, r.mode)
	if err := sleepCtx(ctx, wait); err != nil {
		return err
	}

	svc := r.findServiceName()
	if svc == "" {
		return errors.New("could not determine service name from azure.yaml")
	}
	r.t.Logf("service name: %s", svc)

	// The invocations protocol requires a JSON body via --input-file.
	payload := filepath.Join(r.testDir, ".invoke-payload.json")
	if err := os.WriteFile(payload, []byte(`{"message": "Hello, what is 2+2?"}`), 0o600); err != nil {
		return fmt.Errorf("write invoke payload: %w", err)
	}

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		r.t.Logf("invoke attempt %d/%d", attempt, maxRetries)
		out, code := r.runAzd(ctx, r.projectDir, invokeTimeout,
			"ai", "agent", "invoke", svc, "--new-session", "-f", payload)

		if code != 0 {
			if attempt == maxRetries {
				logs, _ := r.runAzd(ctx, r.projectDir, monitorTimeout,
					"ai", "agent", "monitor", svc, "--tail", "50")
				r.t.Logf("agent logs (tail):\n%s", tail(logs, 4000))
				return fmt.Errorf("azd invoke failed (exit %d)", code)
			}
			delay := 15 * time.Second
			if strings.Contains(out, "500") ||
				strings.Contains(strings.ToLower(out), "internal server error") {
				delay = 30 * time.Second // container may still be starting
			}
			r.t.Logf("invoke failed (exit %d); retrying in %s", code, delay)
			if err := sleepCtx(ctx, delay); err != nil {
				return err
			}
			continue
		}

		if !responseHasExpectedAnswer(out) {
			if attempt < maxRetries {
				r.t.Log("response missing expected '4'/'four'; retrying")
				if err := sleepCtx(ctx, 15*time.Second); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("invoke response missing expected '4'/'four': %s", truncate(out, 200))
		}

		r.t.Log("invoke succeeded; response contains the expected answer")
		return nil
	}
	return errors.New("invoke failed after all retries")
}

// teardown runs `azd down` so a run never leaves billable resources behind. It
// uses a fresh context because the per-run deadline may already have fired.
func (r *runner) teardown() {
	if r.projectDir == "" {
		r.projectDir = r.findProjectDir()
	}
	if r.projectDir == "" {
		return
	}
	r.t.Log("teardown: azd down --force --purge")
	_, code := r.runAzd(context.Background(), r.projectDir, teardownTimeout,
		"down", "--force", "--purge", "--no-prompt")
	if code != 0 {
		r.t.Errorf("azd down failed (exit %d) — Azure resources may be leaked", code)
	}
}

// runAzd runs an azd command in dir with a timeout, streaming combined output to
// the test log and returning it along with the exit code.
func (r *runner) runAzd(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, int) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // azd is a trusted fixed binary; args are test-controlled.
	cmd := exec.CommandContext(cctx, "azd", args...)
	cmd.Dir = dir
	cmd.Env = r.env

	var buf bytes.Buffer
	lw := &lineLogger{t: r.t}
	cmd.Stdout = io.MultiWriter(&buf, lw)
	// Same writer value as Stdout => os/exec uses one pipe and one copier
	// goroutine, so there is no concurrent write to buf/lw.
	cmd.Stderr = cmd.Stdout

	err := cmd.Run()
	lw.flush()
	return buf.String(), exitCode(err)
}

// selectByText filters a survey list by typing target, waits (event-driven) for
// the filtered list to stop redrawing, then confirms with Enter. This assumes
// the survey / azd-core Select supports type-to-filter; that behavior is only
// verifiable against a live run (documented in README). waitForQuiet's exited
// result is intentionally ignored: a child that exited mid-select makes the
// trailing Enter a harmless no-op on the closed pty.
func (r *runner) selectByText(target string) {
	r.c.send(target)
	r.c.waitForQuiet(listSettle)
	r.c.send(keyEnter)
}

// enter accepts a prompt's default by pressing Enter.
func (r *runner) enter() {
	r.c.send(keyEnter)
}

// createProject reports whether the run should create a fresh Foundry project.
func (r *runner) createProject() bool {
	return envTrue("E2E_CREATE_PROJECT")
}

// findProjectDir returns the first immediate subdirectory of testDir that
// contains an azure.yaml (the project scaffolded by init), or "".
func (r *runner) findProjectDir() string {
	entries, err := os.ReadDir(r.testDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(r.testDir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "azure.yaml")); err == nil {
			return dir
		}
	}
	return ""
}

// findServiceName reads the service name from the project's azure.yaml. azd
// scaffolds exactly one service, so the sole key under services: is the name.
func (r *runner) findServiceName() string {
	dir := r.projectDir
	if dir == "" {
		dir = r.findProjectDir()
	}
	if dir == "" {
		return ""
	}
	//nolint:gosec // azure.yaml path is under the test-controlled testDir.
	data, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	if err != nil {
		return ""
	}
	// A struct unmarshal is more robust than scanning lines: it tolerates
	// comments and indentation changes that a naive parser would mishandle.
	var proj struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &proj); err != nil || len(proj.Services) == 0 {
		return ""
	}
	for name := range proj.Services {
		return name
	}
	return ""
}

// validateInitOutput confirms init produced an agent project on disk: a project
// dir whose azure.yaml targets the agent host and a nested agent.yaml.
func (r *runner) validateInitOutput() bool {
	entries, err := os.ReadDir(r.testDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdir := filepath.Join(r.testDir, e.Name())
		//nolint:gosec // azure.yaml path is under the test-controlled testDir.
		data, err := os.ReadFile(filepath.Join(subdir, "azure.yaml"))
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, "host:") && strings.Contains(content, "azure.ai.agent") &&
			hasAgentYAML(subdir) {
			return true
		}
	}
	return false
}

// hasAgentYAML reports whether an agent.yaml exists anywhere under root.
func hasAgentYAML(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "agent.yaml" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// lineLogger forwards a stream to t.Log one line at a time so long-running azd
// output is visible live in the CI log.
type lineLogger struct {
	t   *testing.T
	buf []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.t.Log(strings.TrimRight(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

func (l *lineLogger) flush() {
	if len(l.buf) > 0 {
		l.t.Log(strings.TrimRight(string(l.buf), "\r"))
		l.buf = nil
	}
}

// exitCode extracts a process exit code from an exec error (-1 if it never ran).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return -1
}

// ghToken resolves a GitHub token from the environment, falling back to `gh`.
func ghToken() string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	//nolint:gosec // gh is a trusted fixed binary; no user input in args.
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shortHash returns a short, non-cryptographic uniqueness suffix for the agent
// name (sha256 only to avoid noise from security scanners).
func shortHash(mode string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s-%d", mode, os.Getpid()))
	return hex.EncodeToString(sum[:])[:6]
}

// assertSafeTestDir refuses a path that is not clearly a disposable test dir, so
// a bad E2E_TESTDIR (e.g. "/", "/tmp", "$HOME") can never trigger a destructive
// delete.
func assertSafeTestDir(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve test dir: %w", err)
	}
	abs = filepath.Clean(abs)
	protected := map[string]bool{
		"/": true, "/tmp": true, "/var": true, "/usr": true, "/etc": true,
		"/bin": true, "/lib": true, "/root": true, "/home": true,
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		protected[filepath.Clean(home)] = true
	}
	if protected[abs] || strings.Count(abs, "/") < 2 {
		return fmt.Errorf("refusing to delete unsafe test dir %q (resolved %q)", path, abs)
	}
	return nil
}

// useAzCliAuth reports whether to use the az CLI session for azd auth (CI), as
// opposed to azd's built-in auth (local WSL).
func useAzCliAuth() bool {
	return envTrue("E2E_USE_AZ_CLI_AUTH") ||
		os.Getenv("GITHUB_ACTIONS") != "" ||
		os.Getenv("TF_BUILD") != ""
}

// getenvDefault returns the env var value, or def if unset/empty.
func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envTrue reports whether an env var is set to a truthy value.
func envTrue(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first, returning ctx.Err() then.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncate trims s and caps it to n characters with an ellipsis.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// tail returns the last n bytes of s with a leading ellipsis when truncated.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
