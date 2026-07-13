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
	tagTimeout       = 2 * time.Minute

	// deleteAfterRetention is how far ahead the DeleteAfter cleanup tag is set
	// on the provisioned resource group. It must exceed a full run so a healthy
	// in-flight test is never reclaimed, with margin to inspect a failed run
	// before the EngSys garbage collector deletes it.
	deleteAfterRetention = 48 * time.Hour

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
//
// E2E_SKIP_DEPLOY=true runs only init → provision → down, skipping deploy and
// invoke. Those phases don't affect the interactive `azd down` teardown, so
// this is the fast way to iterate on the provision/down path (issue #8839)
// without paying for a full agent deploy.
func (r *runner) run(ctx context.Context) {
	if err := r.phaseInitWithTokenCheck(ctx); err != nil {
		r.t.Errorf("init: %v", err)
		return
	}
	if err := r.phaseProvision(ctx); err != nil {
		r.t.Errorf("provision: %v", err)
		return
	}
	if envTrue("E2E_SKIP_DEPLOY") {
		r.t.Log("E2E_SKIP_DEPLOY=true: skipping deploy and invoke phases")
	} else {
		if err := r.phaseDeploy(ctx); err != nil {
			r.t.Errorf("deploy: %v", err)
			return
		}
		if err := r.phaseInvoke(ctx); err != nil {
			r.t.Errorf("invoke: %v", err)
			return
		}
	}
	if err := r.phaseDown(ctx); err != nil {
		r.t.Errorf("down: %v", err)
		return
	}
}

// phaseInitWithTokenCheck keeps the init failure mode explicit when a bad GitHub
// token from the pipeline environment causes gh to fail before the public sample
// is downloaded. Retrying without the token is unsafe in CI: gh can fall back to
// an interactive browser/device-login flow that the PTY driver would otherwise
// keep answering.
func (r *runner) phaseInitWithTokenCheck(ctx context.Context) error {
	err := r.phaseInit(ctx)
	if err == nil || !isInvalidGitHubTokenError(err) {
		return err
	}

	return fmt.Errorf(
		"init failed because GH_TOKEN/GITHUB_TOKEN is invalid; refusing to retry without token "+
			"because gh can prompt for interactive authentication in CI: %w",
		err,
	)
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
	// flow: promptDeployMode (init_from_code.go) auto-resolves it to "container"
	// when a manifest is provided, so it must be chosen via newInitCommand's
	// --deploy-mode flag (init.go). r.mode is exactly "container" or "code".
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
// prints (each case annotated with the source function that prints it).
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
			if strings.Contains(prompt, "subscription") || strings.Contains(prompt, "location") ||
				strings.Contains(prompt, "region") {
				r.t.Log("loop detected on picker prompt; accepting current selection")
				r.enter()
				continue
			}
			// The model select/deployment prompts can legitimately need a
			// different option than the default (e.g. the manifest model isn't
			// available in the chosen region), so nudge to the next choice. Scope
			// this strictly to the model prompts: the Foundry-project prompt text
			// ("...host your agent and any models or tools it uses.") also contains
			// "model", and must NOT be treated as a model prompt here.
			isModelPrompt := strings.Contains(prompt, "select a model") ||
				strings.Contains(prompt, "is specified in the agent manifest")
			if isModelPrompt {
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

		if err := r.dispatchPrompt(screen, prompt); err != nil {
			return err
		}
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
		"init finished but expected artifacts are missing on disk\n--- test dir ---\n%s\n--- tail ---\n%s",
		r.initOutputDiagnostics(), tail(r.c.tailString(), 2000),
	)
}

// isInitComplete reports whether the success marker is on screen. Source:
// runInitFromManifest (init.go) prints "AI agent definition added to your azd
// project successfully!" in green at the end.
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
// generic and keyed on the verbatim messages the extension prints; the function
// in each comment points at the source string this matches. The prompt argument
// is already lowercased (see activePrompt).
//
// Only a subset of these fire on the --agent-name template critical path
// (language, template, Foundry project, subscription, location, the manifest
// model, deployment name, capacity/sku/version). The rest are kept as defensive
// handlers because init auto-resolves them under userProvidedManifest=true (so
// they normally do NOT prompt) or only surfaces them for specific runtime state.
func (r *runner) dispatchPrompt(screen, prompt string) error {
	has := func(sub string) bool { return strings.Contains(prompt, sub) }

	switch {
	case isGitHubAuthPrompt(prompt):
		return fmt.Errorf(
			"init reached interactive GitHub CLI authentication prompt: %q; "+
				"check GH_TOKEN/GITHUB_TOKEN for the live pipeline",
			prompt,
		)

	// Yes/No confirms. "Continue with this existing agent name?"
	// (resolveExistingAgentNameConflictWithChecker) only fires when the unique
	// name already exists; decline it to reach the fresh-name input. Any other
	// confirm: accept.
	case has("[y/n]") || has("(y/n)") || has("continue with this existing agent name"):
		if has("continue with this existing agent name") {
			r.c.send("n")
		} else {
			r.c.send("y")
		}
		r.c.send(keyEnter)

	// Language select — "Select a language" (promptAgentTemplate).
	case has("select a language"):
		r.selectByText(screen, "Python")

	// Template select — "Select a starter template" / "Select an agent template"
	// (promptAgentTemplate).
	case has("starter template") || has("agent template"):
		r.selectByText(screen, "Basic agent (Invocations")

	// Foundry project hosting — "Select a Foundry project to host your agent..."
	// (runInitFromManifest); choices "Use an existing..." / "Create a new...".
	case has("foundry project to host"):
		if r.createProject() {
			r.selectByText(screen, "Create a new Foundry project")
		} else {
			r.selectByText(screen, "Use an existing Foundry project")
		}

	// Existing-project picker — "Select a Foundry project"
	// (selectFoundryProject); only when reusing a project.
	case has("select a foundry project"):
		if p := os.Getenv("E2E_PROJECT"); p != "" {
			r.selectByText(screen, p)
		} else {
			r.enter()
		}

	// Subscription — the extension prints a descriptive preamble via fmt.Println
	// (runInitFromManifest), but that line isn't the survey "?" line activePrompt
	// reads. ensureSubscription passes an empty request, so the picker shows
	// azd-core's default message "Select subscription" (promptSubscriptionMessage)
	// — match that, not the preamble.
	case has("select subscription"):
		if sub := os.Getenv("E2E_SUBSCRIPTION"); sub != "" {
			r.selectByText(screen, sub[:min(8, len(sub))])
		} else {
			r.enter()
		}

	// Location — preamble "Select an Azure location..." (ensureLocation) +
	// azd-core picker.
	case has("location") || has("region"):
		r.selectByText(screen, getenvDefault("E2E_LOCATION", "eastus2"))

	// Manifest model decision — "Model '%s' is specified in the agent manifest."
	// (getModelDetails); keep the manifest model (default first choice).
	case has("is specified in the agent manifest"):
		r.enter()

	// Existing deployments / generic proceed — getModelDeploymentDetails.
	case has("how would you like to proceed") || has("existing deployment"):
		r.enter()

	// Model deployment name input — getModelDeploymentDetails (default = model name).
	case has("model deployment name") || (has("deployment name") && has("model")):
		r.enter()

	// Model select — "Select a model" (promptForAlternativeModel etc.).
	case has("select a model"):
		r.selectByText(screen, "gpt-4o-mini")

	// Deployment version / SKU / capacity — azd-core's PromptAiDeployment renders
	// these exact picker messages; accept defaults. Match the full message rather
	// than the bare keyword so a future prompt merely containing
	// "version"/"sku"/"capacity" can't match by accident (it would fall through to
	// the logged default instead).
	case has("select a version for") || has("select a sku for") ||
		has("enter deployment capacity for"):
		r.enter()

	// Code-deploy prompts (promptCodeConfig). Auto-resolved under
	// userProvidedManifest=true, so kept as defensive handlers only.
	case has("select the runtime for your agent"):
		r.enter() // default Python 3.13
	case has("entry point"):
		r.enter() // accept detected default
	case has("how should dependencies be resolved"):
		r.enter() // default remote build

	// Optional infra (blank => create new): ACR login server
	// (configureAcrConnection), App Insights (configureAppInsightsConnection).
	case has("acr login server") || has("container registry"):
		r.enter()
	case has("application insights"):
		r.enter()

	// Startup command (resolveStartupCommandForInit); blank => skip.
	case has("command to start your agent"):
		r.enter()

	// Replacement agent name after declining the existing-name confirm
	// (promptForReplacementAgentName) / the name input (resolveInitAgentName);
	// accept the default.
	case has("enter a different name for your agent") || has("enter a name for your agent"):
		r.enter()

	default:
		// No specific case matched: send Enter as a safe default, but log the
		// fall-through so CI can distinguish "matched and answered correctly"
		// from "hit the catch-all" when a new or changed prompt appears.
		r.t.Logf("unhandled prompt (default Enter): %s", truncate(prompt, 100))
		r.enter()
	}

	return nil
}

func isGitHubAuthPrompt(prompt string) bool {
	prompt = strings.ToLower(prompt)
	return strings.Contains(prompt, "preferred protocol for git operations") ||
		strings.Contains(prompt, "authenticate git with your github credentials") ||
		strings.Contains(prompt, "authenticate github cli") ||
		strings.Contains(prompt, "login with a web browser") ||
		strings.Contains(prompt, "github.com/login/device")
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

	// Stamp the resource group with a DeleteAfter cleanup tag as soon as it
	// exists. The post-run `azd down` teardown is the primary cleanup, but it is
	// unreliable in CI (the agent can exhaust its post-timeout budget, crash
	// mid-delete, or lose its network connection); the tag lets the EngSys
	// garbage collector reclaim the group regardless. Best-effort: never fails.
	r.tagResourceGroupForCleanup(ctx)
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

		if !responseHasExpectedAnswer(agentResponseRegion(out)) {
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

// phaseDown exercises the interactive `azd down` teardown. With no --force flag
// the Foundry provider must prompt for confirmation (issue #8839) instead of
// failing outright. This drives the confirmation prompt over a pseudo-terminal,
// verifies it names the resource group, answers "yes", waits for the delete to
// finish, and asserts the resource group is actually gone. The registered
// force+purge teardown remains as an idempotent safety net.
func (r *runner) phaseDown(ctx context.Context) error {
	// Capture the resource group + subscription BEFORE deleting, so we can
	// verify the group is gone afterward: `azd down` invalidates the env values.
	vals := r.azdEnvValues(ctx)
	rg := vals["AZURE_RESOURCE_GROUP"]
	sub := vals["AZURE_SUBSCRIPTION_ID"]
	if rg == "" {
		return errors.New("AZURE_RESOURCE_GROUP not found in azd env; cannot exercise interactive down")
	}

	if err := r.runAzdDownInteractive(ctx, rg); err != nil {
		return err
	}

	// Verify the resource group was actually deleted. `az group exists` prints
	// "true"/"false". A verification hiccup (az error) is logged but not fatal —
	// the force teardown safety net still guarantees cleanup — but a group that
	// still exists means the interactive down did not actually tear down.
	args := []string{"group", "exists", "--name", rg}
	if sub != "" {
		args = append(args, "--subscription", sub)
	}
	out, code := r.runQuiet(ctx, r.projectDir, tagTimeout, "az", args...)
	if code != 0 {
		r.t.Logf("warning: could not verify resource group deletion (exit %d): %s",
			code, truncate(strings.TrimSpace(out), 200))
		return nil
	}
	if strings.Contains(strings.ToLower(out), "true") {
		return fmt.Errorf("resource group %q still exists after interactive `azd down`", rg)
	}
	r.t.Logf("interactive `azd down` deleted resource group %q", rg)
	return nil
}

// runAzdDownInteractive runs `azd down --purge` (no --force) attached to a
// pseudo-terminal and drives the destroy confirmation prompt to completion.
// --purge is included so soft-deleted Cognitive Services accounts don't block a
// later re-provision; the confirmation prompt fires regardless of --purge.
func (r *runner) runAzdDownInteractive(ctx context.Context, rg string) error {
	c, err := newConsole(initCols, initRows)
	if err != nil {
		return err
	}
	defer c.close()

	dctx, cancel := context.WithTimeout(ctx, teardownTimeout)
	defer cancel()

	//nolint:gosec // azd is a trusted fixed binary; args are test-controlled.
	cmd := exec.CommandContext(dctx, "azd", "down", "--purge")
	cmd.Dir = r.projectDir
	cmd.Env = r.env
	cmd.Stdin = c.tty()
	cmd.Stdout = c.tty()
	cmd.Stderr = c.tty()
	// Give the child the pts as its controlling terminal so the prompt library
	// treats it as a real interactive terminal (mirrors phaseInit).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start azd down: %w", err)
	}

	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()

	confirmed, driveErr := r.driveDown(dctx, c, exited, rg)

	// Ensure the child is gone before returning (it normally exits itself).
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}

	if driveErr != nil {
		return driveErr
	}
	if !confirmed {
		return fmt.Errorf("azd down exited without prompting for confirmation\n--- tail ---\n%s",
			tail(c.tailString(), 2000))
	}
	if code := exitCode(waitErr); code != 0 {
		return fmt.Errorf("azd down (interactive) failed (exit %d)\n--- tail ---\n%s",
			code, tail(c.tailString(), 2000))
	}
	return nil
}

// driveDown waits for the Foundry destroy confirmation prompt, verifies it names
// the resource group, and answers "yes". It returns confirmed=true once it has
// answered, then keeps draining output (rendering the delete progress) until the
// child exits or ctx is cancelled.
func (r *runner) driveDown(
	ctx context.Context, c *console, exited <-chan struct{}, rg string,
) (confirmed bool, err error) {
	for {
		select {
		case <-ctx.Done():
			return confirmed, fmt.Errorf("azd down timed out: %w\n--- tail ---\n%s",
				ctx.Err(), tail(c.tailString(), 2000))
		case <-exited:
			return confirmed, nil
		default:
		}

		// Block until the UI stops emitting (prompt drawn, awaiting input) or
		// the child exits.
		if c.waitForQuiet(promptQuiet) {
			return confirmed, nil // child exited
		}
		if confirmed {
			continue // already answered; just drain until exit
		}

		screen := c.screen()
		// Only answer once survey is actively blocking on a "?" prompt.
		if activePrompt(screen) == "" {
			continue
		}
		if !isDestroyConfirmScreen(screen) {
			continue
		}

		r.t.Log("down: destroy confirmation prompt detected")
		if !screenContains(screen, rg) {
			return confirmed, fmt.Errorf(
				"destroy confirmation did not name resource group %q\n--- screen ---\n%s", rg, screen)
		}
		c.send("y" + keyEnter)
		confirmed = true
	}
}

// isDestroyConfirmScreen reports whether the rendered screen shows the Foundry
// provider's destroy confirmation prompt. Source: confirmDestroy in
// foundry_provisioning_provider.go prints "... Are you sure you want to
// continue?" as a survey Confirm.
func isDestroyConfirmScreen(screen string) bool {
	return screenContains(screen, "are you sure you want to continue") &&
		screenContains(screen, "delete resource group")
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

// tagResourceGroupForCleanup best-effort stamps a DeleteAfter tag on the
// provisioned resource group so the EngSys garbage collector can find and
// delete it even when the explicit `azd down` teardown never runs. Failures are
// logged and ignored: the tag is a safety net layered on top of teardown, not a
// gate on the test. See the EngSys resource-management spec for the tag format.
func (r *runner) tagResourceGroupForCleanup(ctx context.Context) {
	vals := r.azdEnvValues(ctx)
	rg := vals["AZURE_RESOURCE_GROUP"]
	if rg == "" {
		r.t.Log("skip DeleteAfter tag: AZURE_RESOURCE_GROUP not found in azd env")
		return
	}
	// EngSys expects an RFC 3339 / ISO 8601 UTC instant; the group is reclaimed
	// once that time has passed. `--set tags.DeleteAfter=` adds just this one
	// tag, leaving azd's own tags (e.g. azd-env-name) intact.
	deleteAfter := time.Now().UTC().Add(deleteAfterRetention).Format(time.RFC3339)
	args := []string{"group", "update", "--name", rg,
		"--set", "tags.DeleteAfter=" + deleteAfter, "--output", "none"}
	if sub := vals["AZURE_SUBSCRIPTION_ID"]; sub != "" {
		args = append(args, "--subscription", sub)
	}
	if out, code := r.runQuiet(ctx, r.projectDir, tagTimeout, "az", args...); code != 0 {
		r.t.Logf("warning: could not tag resource group %q with DeleteAfter (exit %d): %s",
			rg, code, truncate(strings.TrimSpace(out), 200))
		return
	}
	r.t.Logf("tagged resource group %q with DeleteAfter=%s", rg, deleteAfter)
}

// azdEnvValues returns the project's azd environment as a key→value map. Output
// is captured quietly (never streamed to the test log) because azd env values
// can include provisioning secrets. A failure yields an empty map.
func (r *runner) azdEnvValues(ctx context.Context) map[string]string {
	out, code := r.runQuiet(ctx, r.projectDir, tagTimeout, "azd", "env", "get-values")
	vals := map[string]string{}
	if code != 0 {
		r.t.Logf("warning: azd env get-values failed (exit %d)", code)
		return vals
	}
	// Lines are KEY="value"; Cut on the first '=' so values containing '=' are
	// preserved, then strip the surrounding quotes azd always emits.
	for line := range strings.SplitSeq(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"`)
	}
	return vals
}

// runQuiet runs name+args in dir with a timeout and returns combined output and
// exit code WITHOUT streaming to the test log. Used for commands whose output
// may carry secrets (`azd env get-values`) or is pure side effect (`az group
// update`).
func (r *runner) runQuiet(
	ctx context.Context, dir string, timeout time.Duration, name string, args ...string,
) (string, int) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // name and args are fixed, test-controlled values.
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	return string(out), exitCode(err)
}

// selectByText filters a survey list by typing target unless the target is
// already selected, then confirms with Enter. Some survey redraws briefly expose
// the same prompt after a selection is accepted; treating an already-selected
// target as done keeps the driver idempotent and avoids appending stale filter
// text into the next prompt.
func (r *runner) selectByText(screen, target string) {
	if currentSelectionHas(screen, target) {
		r.enter()
		return
	}
	r.clearFilter()
	r.c.send(target)
	r.c.waitForQuiet(listSettle)
	r.c.send(keyEnter)
}

// enter accepts a prompt's default by pressing Enter.
func (r *runner) enter() {
	r.c.send(keyEnter)
}

// clearFilter removes any stale type-to-filter text left in the active survey
// prompt before typing a new target. Ctrl+U covers readline-style inputs; Del is
// repeated as a fallback for survey prompts that treat the filter as a simple
// editable field.
func (r *runner) clearFilter() {
	r.c.send(keyCtrlU)
	r.c.send(strings.Repeat(keyDel, 64))
}

func currentSelectionHas(screen, target string) bool {
	target = strings.ToLower(target)
	for _, line := range nonEmptyLines(screen) {
		line = strings.ToLower(line)
		if strings.HasPrefix(line, "?") && strings.Contains(line, target) {
			return true
		}
		if strings.Contains(line, ">") && strings.Contains(line, target) {
			return true
		}
	}
	return false
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
	var proj azdProjectManifest
	if err := yaml.Unmarshal(data, &proj); err != nil || len(proj.Services) == 0 {
		return ""
	}
	for name, svc := range proj.Services {
		if svc.Host == "azure.ai.agent" {
			return name
		}
	}
	for name := range proj.Services {
		return name
	}
	return ""
}

type azdProjectManifest struct {
	Services map[string]azdService `yaml:"services"`
}

type azdService struct {
	Host string `yaml:"host"`
}

// validateInitOutput confirms init produced an azd project on disk. Current
// unified azure.yaml samples can inline the agent definition in azure.yaml
// instead of writing a separate agent.yaml, so the stable contract is that init
// created a project directory with a parseable azure.yaml containing services.
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
		if hasProjectManifest(subdir) {
			return true
		}
	}
	return false
}

// hasProjectManifest reports whether root contains a parseable azd project
// manifest with an azure.ai.agent service. Current unified azure.yaml samples
// can inline the agent definition there rather than writing agent.yaml.
func hasProjectManifest(root string) bool {
	//nolint:gosec // azure.yaml path is under the test-controlled testDir.
	data, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
	if err != nil {
		return false
	}
	var proj azdProjectManifest
	if err := yaml.Unmarshal(data, &proj); err != nil {
		return false
	}
	for _, svc := range proj.Services {
		if svc.Host == "azure.ai.agent" {
			return true
		}
	}
	return false
}

// initOutputDiagnostics returns a bounded file listing to make scaffold-layout
// drift visible in CI logs without dumping generated source files.
func (r *runner) initOutputDiagnostics() string {
	var b strings.Builder
	count := 0
	const maxEntries = 80

	err := filepath.WalkDir(r.testDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", path, err)
			return nil
		}
		rel, relErr := filepath.Rel(r.testDir, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if count >= maxEntries {
			return filepath.SkipAll
		}
		if strings.Count(filepath.ToSlash(rel), "/") > 3 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		fmt.Fprintln(&b, rel)
		count++
		return nil
	})
	if err != nil {
		fmt.Fprintf(&b, "walk failed: %v\n", err)
	}
	if count >= maxEntries {
		fmt.Fprintf(&b, "... truncated after %d entries\n", maxEntries)
	}
	return strings.TrimSpace(b.String())
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

func isInvalidGitHubTokenError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "The token in GH_TOKEN is invalid") ||
		strings.Contains(msg, "The token in GITHUB_TOKEN is invalid")
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
