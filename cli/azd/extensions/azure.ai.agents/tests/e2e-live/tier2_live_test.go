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

	initStepDelay = 3 * time.Second
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
			ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
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
		_, _ = r.runAzd(context.Background(), testDir, time.Minute,
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

	args := []string{"ai", "agent", "init", "--agent-name", r.agentName}
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

	// Render child output to the screen for the whole lifetime of the process.
	go c.drain()

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

// driveInit is the prompt state machine: it polls the rendered screen and
// answers each survey prompt until init reports completion (or the process
// exits, or it times out).
func (r *runner) driveInit(ctx context.Context, exited <-chan struct{}) error {
	var lastPromptKey string
	samePromptCount := 0

	for {
		if err := sleepCtx(ctx, initStepDelay); err != nil {
			return fmt.Errorf("init timed out: %w", err)
		}

		select {
		case <-exited:
			if r.validateInitOutput() {
				return nil
			}
			return errors.New("azd ai agent init exited without producing artifacts")
		default:
		}

		screen := r.c.screen()

		if screenContains(screen, "added to your azd project") ||
			screenContains(screen, "agent definition added") {
			if r.validateInitOutput() {
				return nil
			}
			if err := sleepCtx(ctx, 5*time.Second); err != nil {
				return err
			}
			if r.validateInitOutput() {
				return nil
			}
			return errors.New("init completion marker found but artifacts missing on disk")
		}

		prompt := activePrompt(screen)
		if prompt == "" {
			continue
		}
		r.t.Logf("prompt: %s", truncate(prompt, 100))

		// Loop detection: compare the question text before ':' so varying filter
		// text on the same prompt doesn't reset the counter.
		key := prompt
		if i := strings.Index(prompt, ":"); i > 0 {
			key = strings.TrimSpace(prompt[:i])
		}
		if key == lastPromptKey {
			samePromptCount++
		} else {
			samePromptCount = 1
			lastPromptKey = key
		}
		if samePromptCount >= 3 {
			if strings.Contains(prompt, "model") || strings.Contains(prompt, "is specified") {
				r.t.Log("loop detected on model prompt; trying next option")
				r.c.send(keyDown)
				time.Sleep(300 * time.Millisecond)
				r.c.send(keyEnter)
				continue
			}
			if samePromptCount >= 5 {
				return fmt.Errorf("init stuck in prompt loop: %q", key)
			}
		}

		r.dispatchPrompt(screen, prompt)
	}
}

// dispatchPrompt answers a single survey prompt. The case order mirrors the
// original Python elif chain: more specific prompts must precede generic ones.
func (r *runner) dispatchPrompt(screen, prompt string) {
	contains := func(sub string) bool { return strings.Contains(prompt, sub) }

	switch {
	case contains("[y/n]") || contains("(y/n)"):
		if contains("continue with this existing agent name") {
			r.c.send("n") // use a fresh name
		} else {
			r.c.send("y")
		}
		r.c.send(keyEnter)
	case contains("language"):
		r.selectByText("Python", 1500*time.Millisecond)
	case contains("template"):
		r.selectByText("Basic agent (Invocations", 1500*time.Millisecond)
	case contains("protocol") || contains("git operations"):
		r.enter() // HTTPS (default)
	case contains("enter a different name"):
		r.enter()
	case contains("container registry") || contains("acr"):
		r.enter() // blank -> create new
	case contains("model deployment name") ||
		(contains("enter") && contains("deployment") && contains("name")):
		r.enter()
	case contains("existing deployment") || contains("is specified in the agent manifest") ||
		(contains("found") && contains("deployment")):
		r.enter()
	case contains("capacity"):
		r.enter()
	case contains("sku"):
		r.enter()
	case contains("version"):
		r.enter()
	case contains("select") && contains("model"):
		r.selectByText("gpt-4o-mini", 1500*time.Millisecond)
	case contains("subscription"):
		if sub := os.Getenv("E2E_SUBSCRIPTION"); sub != "" {
			r.selectByText(sub[:min(8, len(sub))], 2*time.Second)
		} else {
			r.enter()
		}
	case contains("location") || contains("region"):
		r.selectByText(getenvDefault("E2E_LOCATION", "eastus2"), 2*time.Second)
	case contains("foundry project") || (contains("select") && contains("project")):
		switch {
		case r.createProject() && screenContains(screen, "create a new"):
			r.selectByText("Create", 3*time.Second)
		case os.Getenv("E2E_PROJECT") != "":
			r.selectByText(os.Getenv("E2E_PROJECT"), 3*time.Second)
		default:
			r.enter()
		}
	case contains("account name") || contains("resource name") || contains("hub name"):
		r.enter()
	case contains("model") && !contains("capacity"):
		r.enter()
	case contains("deploy") && (contains("mode") || contains("how")) && !contains("capacity"):
		if r.mode == "container" {
			r.selectByText("Container", 1500*time.Millisecond)
		} else {
			r.selectByText("Source", 1500*time.Millisecond)
		}
	case contains("what would you like to do"):
		r.enter() // Exit setup (default)
	case contains("enter a name"):
		r.enter()
	default:
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

// selectByText filters a survey list by typing target, waits for the list to
// settle, then confirms with Enter.
func (r *runner) selectByText(target string, delay time.Duration) {
	r.c.send(target)
	time.Sleep(delay)
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

// findServiceName reads the first service name from the project's azure.yaml.
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
	inServices := false
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "services:" {
			inServices = true
			continue
		}
		if inServices && strings.HasPrefix(line, "  ") && strings.HasSuffix(trimmed, ":") {
			return strings.TrimSuffix(trimmed, ":")
		}
		if inServices && !strings.HasPrefix(line, " ") && trimmed != "" {
			break
		}
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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
