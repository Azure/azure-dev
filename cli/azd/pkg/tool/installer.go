// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// wingetNoPackageFoundExitCode is winget's
// APPINSTALLER_CLI_ERROR_NO_APPLICATIONS_FOUND ("No packages found"),
// returned when no installed package matches the uninstall criteria. It is
// locale-independent, unlike the printed "No installed package found
// matching input criteria." text. Windows reports it as the unsigned DWORD
// 0x8A150014; Go's os/exec surfaces it as the int 2316632084, while shells
// display the signed -1978335212. Comparing via uint32 matches all
// representations. See:
// https://github.com/microsoft/winget-cli/blob/master/doc/windows/package-manager/winget/returnCodes.md
const wingetNoPackageFoundExitCode uint32 = 0x8A150014

// InstallResult captures the outcome of an install or upgrade operation.
type InstallResult struct {
	// Tool is the definition that was installed or upgraded.
	Tool *ToolDefinition
	// Success indicates whether the operation completed successfully
	// and the tool is now available on the local machine.
	Success bool
	// InstalledVersion is the version detected after installation.
	InstalledVersion string
	// AlreadyUpToDate is set for an upgrade when every targeted agent
	// reported the skill was already at the latest version, so nothing was
	// changed. The command layer uses it to show an "already up to date"
	// result instead of an "upgraded" one.
	AlreadyUpToDate bool
	// Strategy describes what was used to install the tool
	// (e.g. "winget", "brew", "manual").
	Strategy string
	// Duration is the wall-clock time the operation took.
	Duration time.Duration
	// Error holds any error encountered during the operation.
	Error error
}

// AggregateInstallResults summarizes a batch install or upgrade outcome for
// telemetry. It returns success and failure counts plus the sorted IDs of
// failed tools (sorted so N! permutations of the same set collapse to a
// single canonical value, keeping attribute cardinality bounded).
//
// When opErr is non-nil and results is empty (a batch-infrastructure failure
// where no per-tool work was recorded), failures are synthesized for every
// entry in requestedIDs so the aggregate reflects that every requested tool
// effectively failed, instead of silently reporting zero. On this synthesized
// path the invariant successCount + failureCount == len(requestedIDs) holds.
//
// When opErr is non-nil but results is populated, the per-tool entries are
// trusted as-is (the per-tool failures were already recorded) and no
// synthesis happens — synthesizing on top would double-count.
//
// Callers are responsible for emitting the returned values via their own
// attribute keys (tool.* vs tool.firstrun.* etc.).
func AggregateInstallResults(
	results []*InstallResult,
	opErr error,
	requestedIDs []string,
) (successCount, failureCount int, sortedFailedIDs []string) {
	sortedFailedIDs = make([]string, 0, len(results)+len(requestedIDs))
	if opErr != nil && len(results) == 0 {
		failureCount = len(requestedIDs)
		sortedFailedIDs = append(sortedFailedIDs, requestedIDs...)
	} else {
		for _, r := range results {
			if r.Success {
				successCount++
				continue
			}
			failureCount++
			if r.Tool != nil {
				sortedFailedIDs = append(sortedFailedIDs, r.Tool.Id)
			}
		}
	}
	slices.Sort(sortedFailedIDs)
	return
}

// Installer defines the contract for installing and upgrading tools on
// the current platform.
type Installer interface {
	// Install attempts to install the given tool using the best
	// strategy available for the current platform. For skill tools the
	// optional [WithAgents] option restricts installation to the named
	// agent CLIs.
	Install(
		ctx context.Context,
		tool *ToolDefinition,
		opts ...InstallOption,
	) (*InstallResult, error)

	// Upgrade attempts to upgrade the given tool to its latest
	// version. When no upgrade-specific command exists the
	// operation falls back to a regular install.
	Upgrade(
		ctx context.Context,
		tool *ToolDefinition,
		opts ...InstallOption,
	) (*InstallResult, error)

	// AvailableSkillAgents returns the tool's configured SkillAgents that are
	// currently usable (a functional CLI on PATH, per agentUsable), in
	// manifest order (preferred agent first), as two index-aligned slices: the
	// command identities (used for matching) and their display names (shown
	// to the user). Both are nil for non-skill tools or when none of the
	// agents are usable. It probes the agent binaries, so it takes a context.
	AvailableSkillAgents(ctx context.Context, tool *ToolDefinition) (commands []string, names []string)

	// Uninstall removes the given tool from the current platform. For
	// skill tools the optional [WithAgents] option restricts removal to
	// the named agent CLIs; with neither [WithAgents] nor
	// [WithAllAvailableAgents] the skill is removed from every agent it is
	// installed through.
	Uninstall(
		ctx context.Context,
		tool *ToolDefinition,
		opts ...InstallOption,
	) (*InstallResult, error)
}

// installConfig holds the resolved options for an install or upgrade
// operation.
type installConfig struct {
	// agents, when non-empty, restricts a skill install/upgrade to the
	// named agent CLIs (e.g. "copilot", "claude"). An empty slice
	// selects the single preferred agent (the first configured agent on
	// PATH). Ignored for non-skill tools.
	agents []string
	// allAvailableAgents, when true, installs a skill through every
	// configured agent that is on PATH, resolved at install time. It is
	// used by batch flows (e.g. `azd tool install --all`) where the agent
	// CLIs may themselves be installed earlier in the same batch, so the
	// set of available agents is not known until the skill's turn. Ignored
	// when agents is non-empty or for non-skill tools.
	allAvailableAgents bool
	// renderer, when set, renders a live step spinner around each
	// install/upgrade/uninstall step (each agent for a skill tool, or the
	// tool itself otherwise). When nil the
	// installer prints a plain per-agent header instead.
	renderer StepRenderer
	// stdin, when set, is the input reader a skill agent command reads from
	// while a step spinner is showing (so prompts are answered on the same
	// stream the console owns, e.g. Cobra's redirected input). When nil the
	// agent command falls back to the process terminal. Ignored when no
	// spinner is active (the command then runs against the runner's streams).
	stdin io.Reader
}

// InstallOption customizes an install or upgrade operation.
type InstallOption func(*installConfig)

// WithAgents restricts a skill install/upgrade to the named agentic CLI
// agents. It is ignored for non-skill tools. Passing no agents (or not
// using this option) selects the single preferred agent.
func WithAgents(agents ...string) InstallOption {
	return func(c *installConfig) { c.agents = agents }
}

// WithAllAvailableAgents installs a skill through every configured agent
// on PATH, resolved at install time. Use it for batch flows where the
// agent set is not known up front. It is ignored for non-skill tools and
// is overridden by WithAgents.
func WithAllAvailableAgents() InstallOption {
	return func(c *installConfig) { c.allAvailableAgents = true }
}

// StepRenderer renders live per-step progress. It is the subset of
// input.Console the installer uses to show a step spinner per agent,
// matching azd provision/down. input.Console satisfies it.
type StepRenderer interface {
	ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType)
	StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType)
	Message(ctx context.Context, message string)
}

// WithStepProgress renders a live step spinner around each
// install/upgrade/uninstall step via the given renderer (typically the
// console), like azd provision/down. It is opt-in so shared callers that
// manage their own progress (e.g. the first-run middleware) are unaffected.
func WithStepProgress(renderer StepRenderer) InstallOption {
	return func(c *installConfig) { c.renderer = renderer }
}

// WithInput supplies the reader a skill agent command should read stdin from
// while a step spinner is showing — typically the console's input
// (console.Handles().Stdin), so an agent prompt is answered on the same stream
// azd owns rather than the process-global os.Stdin. It is opt-in; without it
// the skill command falls back to the process terminal.
func WithInput(stdin io.Reader) InstallOption {
	return func(c *installConfig) { c.stdin = stdin }
}

// stepError returns the effective error for a step: an operation error, or
// the result's recorded error, or nil.
func stepError(result *InstallResult, err error) error {
	if err != nil {
		return err
	}
	if result != nil {
		return result.Error
	}
	return nil
}

// renderSkillStep frames one skill step (install, upgrade or uninstall) with
// a live spinner that stays visible while the agent CLI runs.
//
// It shows a step spinner (like azd provision) with title and passes work an
// output writer. Skill operations route the agent CLI's stdout/stderr through
// that writer (see skillCommandRunArgs), with stdin still connected.
//
// streamOutput controls how that output is surfaced. When true, the agent
// CLI's output is line-buffered and each complete line is printed above the
// spinner while the spinner stays pinned below it: the console tears the
// spinner down and re-renders it around each printed line (see
// AskerConsole.println), so the bar is kept, not lost. Because a step spinner
// routes the agent CLI's stdout/stderr through a pipe rather than a TTY, the
// supported agent CLIs (copilot, claude) run non-interactively on this path
// and do not emit interactive prompts here; the fully interactive path is the
// renderer==nil branch below, which runs the command directly against the
// terminal. When false, the output is buffered and replayed only if the step
// fails, so a step that completes without error stays quiet. When the CLI
// stays silent the spinner simply runs to completion.
//
// work returns the message to show on the result line; when empty the spinner
// title is reused. This lets a step whose outcome is only known after running
// (e.g. an upgrade that reports the version or "already up to date") show a
// result line that differs from the in-progress title. When renderer is nil
// (e.g. the first-run middleware manages its own progress) it prints a stderr
// header and runs work with a nil writer, so the command runs fully
// interactively.
func renderSkillStep(
	ctx context.Context,
	renderer StepRenderer,
	title string,
	streamOutput bool,
	work func(out io.Writer) (doneTitle string, err error),
) error {
	if renderer == nil {
		fmt.Fprintf(os.Stderr, "\n%s\n", title)
		_, err := work(nil)
		return err
	}

	renderer.ShowSpinner(ctx, title, input.Step)

	// Stream the agent CLI's output live (so interactive prompts are visible),
	// or buffer it and replay only on failure so a successful step is quiet.
	var buffered []string
	emit := func(line string) { renderer.Message(ctx, line) }
	if !streamOutput {
		emit = func(line string) { buffered = append(buffered, line) }
	}
	out := &lineWriter{emit: emit}

	doneTitle, err := work(out)
	out.Flush()
	if doneTitle == "" {
		doneTitle = title
	}
	if !streamOutput && err != nil {
		for _, line := range buffered {
			renderer.Message(ctx, line)
		}
	}
	renderer.StopSpinner(ctx, doneTitle, input.GetStepResultFormat(err))
	return err
}

// lineWriter buffers agent CLI output and hands each complete line to emit.
// Skill agent commands use it to surface the agent CLI's output through the
// console so it prints above the step spinner (which the console re-renders
// around each line), keeping the spinner visible.
//
// os/exec may deliver a single logical line across several Write calls (and
// several lines in one call), so Write accumulates bytes and emits only on a
// newline — never per-write fragments — while preserving empty lines. Bytes
// after the final newline are retained until a later Write completes the line
// or Flush is called. Flush must be called once the command has finished
// writing so a trailing line with no newline (e.g. the CLI's last output line)
// is not lost.
//
// CommandRunner wires a command's stdout and stderr to distinct io.MultiWriter
// values, so os/exec may call Write from two goroutines at once. The mutex
// serializes those calls (and Flush) so emit and the shared buffer are never
// accessed concurrently — avoiding a data race and interleaved/lost agent
// output.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

// Write accumulates p and emits every complete, newline-terminated line via
// emit (without the trailing newline), preserving empty lines. Any bytes after
// the final newline are retained for the next Write or Flush.
func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits any buffered content not terminated by a newline. It is called
// once the command has finished writing so a final line without a trailing
// newline is surfaced rather than dropped.
func (l *lineWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buf) > 0 {
		l.emit(string(l.buf))
		l.buf = nil
	}
}

// skillCommandRunArgs configures how a skill agent command (install, upgrade
// or uninstall) connects to the terminal. When out is non-nil (a step
// spinner is showing) the command's stdout/stderr are routed through it so
// the CLI's output prints above the spinner, and stdin is connected to the
// supplied reader (the console's input, threaded from the step-progress
// caller). Because stdout/stderr are piped (not a TTY) on this path, the
// supported agent CLIs run non-interactively and do not prompt. When out is
// nil (no spinner) the command runs fully interactively against the runner's
// configured streams. azd never pipes canned answers on the user's behalf.
func skillCommandRunArgs(base exec.RunArgs, out io.Writer, stdin io.Reader) exec.RunArgs {
	if out == nil {
		return base.WithInteractive(true)
	}
	if stdin == nil {
		// No input reader supplied (e.g. a non-cmd caller); fall back to the
		// process terminal so prompts remain answerable.
		stdin = os.Stdin
	}
	return base.WithStdIn(stdin).WithStdOut(out).WithStdErr(out)
}

// semverRegex matches a bare semantic version (no leading "v") anywhere in a
// string. Used to pull the version an agent CLI prints after an update.
var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseUpgradeOutput extracts, from an agent CLI's plugin-update output, the
// version it reports and whether it said the plugin was already at the latest
// version. Best-effort across agent CLIs, e.g.:
//
//	copilot: `... updated successfully (v1.1.86, already at latest). Updated 27 skills.`
//	claude:  `... updated from 1.1.73 to 1.1.86 for scope user. ...`
//	claude:  `... azure is already at the latest version (1.1.86).`
//
// The version is the last semver in the output, so an "updated from A to B"
// line yields the new version B. alreadyLatest is true when the output says
// the plugin was already current (nothing changed).
func parseUpgradeOutput(out string) (version string, alreadyLatest bool) {
	lower := strings.ToLower(out)
	alreadyLatest = strings.Contains(lower, "already") &&
		(strings.Contains(lower, "latest") || strings.Contains(lower, "up to date"))
	if m := semverRegex.FindAllString(out, -1); len(m) > 0 {
		version = m[len(m)-1]
	}
	return version, alreadyLatest
}

// installer is the default, unexported implementation of [Installer].
type installer struct {
	commandRunner    exec.CommandRunner
	platformDetector *PlatformDetector
	detector         Detector
	httpClient       httpDoer
	platformMu       sync.Mutex
	platform         *Platform // lazily populated by ensurePlatform
	// agentProbe memoizes agentUsable's version-probe result per agent for the
	// installer's lifetime (one process == one command), so the several
	// agent-resolution call sites do not re-spawn `--version` for the same
	// agent. Only on-PATH results are stored (see agentUsable). Guarded by
	// agentProbeMu.
	agentProbeMu sync.Mutex
	agentProbe   map[string]bool
	// retryBackoff is the initial wait between post-install detection
	// retries (doubled each attempt). Defaults to 1s; tests may shorten
	// it to keep the suite fast.
	retryBackoff time.Duration
	// lookPath resolves an executable's absolute path on PATH. Defaults to
	// os/exec.LookPath; tests may substitute a deterministic resolver.
	lookPath func(string) (string, error)
}

// httpDoer is an interface satisfied by [*http.Client] for testing.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewInstaller creates an [Installer] backed by the provided
// dependencies. Platform detection is deferred until the first
// Install or Upgrade call.
func NewInstaller(
	commandRunner exec.CommandRunner,
	platformDetector *PlatformDetector,
	detector Detector,
) Installer {
	return &installer{
		commandRunner:    commandRunner,
		platformDetector: platformDetector,
		detector:         detector,
		httpClient:       http.DefaultClient,
		retryBackoff:     time.Second,
		lookPath:         osexec.LookPath,
	}
}

// NewInstallerWithHTTPClient creates an [Installer] with a custom
// HTTP client, primarily for testing.
func NewInstallerWithHTTPClient(
	commandRunner exec.CommandRunner,
	platformDetector *PlatformDetector,
	detector Detector,
	httpClient httpDoer,
) Installer {
	return &installer{
		commandRunner:    commandRunner,
		platformDetector: platformDetector,
		detector:         detector,
		httpClient:       httpClient,
		retryBackoff:     time.Second,
		lookPath:         osexec.LookPath,
	}
}

// Install detects the current platform, selects an appropriate
// strategy, runs the installation command, and verifies the result.
func (i *installer) Install(
	ctx context.Context,
	tool *ToolDefinition,
	opts ...InstallOption,
) (*InstallResult, error) {
	return i.run(ctx, tool, false, opts)
}

// Upgrade detects the current platform, selects an appropriate
// strategy, runs the upgrade command, and verifies the result. If
// no upgrade-specific path exists the operation falls back to a
// regular install.
func (i *installer) Upgrade(
	ctx context.Context,
	tool *ToolDefinition,
	opts ...InstallOption,
) (*InstallResult, error) {
	return i.run(ctx, tool, true, opts)
}

// AvailableSkillAgents returns the tool's configured SkillAgents that are
// currently usable, in manifest order (preferred agent first), as two
// index-aligned slices: the command identities (e.g. "copilot", used for
// matching via --agent/findSkillAgent) and their friendly display names (e.g.
// "GitHub Copilot CLI", shown to the user). An agent counts only if
// [installer.agentUsable] confirms it is a functional CLI — not merely a
// same-named launcher stub on PATH — so the interactive agent picker never
// offers an agent the install path would later reject. Both are nil for
// non-skill tools or when none of the agents are usable.
func (i *installer) AvailableSkillAgents(
	ctx context.Context,
	tool *ToolDefinition,
) (commands []string, names []string) {
	if tool.Category != ToolCategorySkill {
		return nil, nil
	}
	for _, agent := range tool.SkillAgents {
		if i.agentUsable(ctx, agent) {
			commands = append(commands, agent.Command)
			names = append(names, agent.DisplayName)
		}
	}
	return commands, names
}

// Uninstall removes a tool from the current platform. Skills are
// removed through their agent plugin command(s); other tools are
// removed via the platform package manager (or by deleting a
// directly-downloaded artifact).
func (i *installer) Uninstall(
	ctx context.Context,
	tool *ToolDefinition,
	opts ...InstallOption,
) (*InstallResult, error) {
	var cfg installConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if tool.Category == ToolCategorySkill {
		// For uninstall, both an explicit `--agent all`
		// (cfg.allAvailableAgents) and an omitted --agent mean "remove the
		// skill from every agent it is installed through" — azd cannot
		// remove a plugin from an agent that never installed it. So only
		// the explicit agent names need to be threaded through; the
		// empty-agents branch of resolveSkillUninstallTargets handles both
		// the default and `--agent all`.
		return i.runSkillUninstall(ctx, tool, cfg.agents, cfg.renderer, cfg.stdin)
	}

	// Non-skill tools uninstall as a single step; render one spinner for
	// the tool (no agent) when a renderer is supplied.
	if cfg.renderer == nil {
		return i.runUninstall(ctx, tool)
	}
	title := fmt.Sprintf("Uninstalling %s", tool.Name)
	cfg.renderer.ShowSpinner(ctx, title, input.Step)
	result, err := i.runUninstall(ctx, tool)
	cfg.renderer.StopSpinner(ctx, title, input.GetStepResultFormat(stepError(result, err)))
	return result, err
}

// runUninstall removes a non-skill tool using the platform's package
// manager (or by deleting a directly-downloaded artifact) and verifies
// the tool is no longer detected. It mirrors run but inverts the
// post-operation verification: success means the tool is gone.
func (i *installer) runUninstall(
	ctx context.Context,
	tool *ToolDefinition,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	// 1. Detect the platform (cached after the first call).
	platform, err := i.ensurePlatform(ctx)
	if err != nil {
		result.Error = fmt.Errorf("detecting platform: %w", err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Select the strategy used to install on this platform. SelectStrategy
	//    returns the install-preferred (first available) strategy.
	strategy := i.platformDetector.SelectStrategy(tool, platform)
	if strategy == nil {
		result.Error = fmt.Errorf(
			"no install strategy for %s on platform %s",
			tool.Name, platform.OS,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2a. For tools with several install methods, the install-preferred
	//     strategy is not necessarily the one that installed the tool. Detect
	//     which method actually owns the install and uninstall via that
	//     (detect-then-remove).
	if strategies := i.platformDetector.SelectStrategies(
		tool, platform,
	); len(strategies) > 1 {
		if detected := i.detectInstallingStrategy(
			ctx, strategies, platform,
		); detected != nil {
			strategy = detected
		} else {
			// No package manager has a record of the tool. If it is still
			// detected on PATH it was installed by a self-contained method
			// (install script / direct download); guide the user to the
			// binary. Otherwise it is already gone.
			return i.uninstallWithoutManagerOwner(ctx, tool, start)
		}
	}

	// 3. Execute the uninstall using the matching mechanism. An explicit
	//    UninstallCommand takes precedence (mirrors how install prefers
	//    InstallCommand), then a package-manager removal; anything else
	//    has no automated uninstall path.
	if strategy.DirectDownloadUrl != "" {
		// Direct download: azd removes the artifact it placed locally.
		result.Strategy = "direct-download"
		if err := i.executeDirectDownloadUninstall(strategy); err != nil {
			result.Error = fmt.Errorf(
				"removing downloaded artifact for %s: %w", tool.Name, err,
			)
			result.Duration = time.Since(start)
			return result, nil
		}
	} else if strategy.UninstallCommand != "" {
		// Explicit uninstall command (e.g. `azd extension uninstall ...`).
		result.Strategy = "command"
		if err := i.executeUninstallCommand(ctx, strategy.UninstallCommand); err != nil {
			// If the tool is already gone, treat the uninstall as complete
			// (idempotent), mirroring the package-manager path below.
			// Otherwise surface the command failure.
			status, detectErr := i.detector.DetectTool(ctx, tool)
			if detectErr == nil && !status.Installed {
				result.Success = true
				result.Duration = time.Since(start)
				return result, nil
			}
			result.Error = fmt.Errorf(
				"running uninstall command for %s: %w", tool.Name, err,
			)
			result.Duration = time.Since(start)
			return result, nil
		}
	} else if strategy.PackageManager != "" && strategy.PackageId != "" {
		result.Strategy = strategy.PackageManager

		// The package manager must be available to remove the tool.
		if !platform.HasManager(strategy.PackageManager) {
			result.Strategy = "manual"
			result.Error = i.managerUnavailableUninstallError(tool, strategy)
			result.Duration = time.Since(start)
			return result, nil
		}

		if res, err := i.executeUninstall(ctx, strategy); err != nil {
			// The package manager could not remove the tool. If the tool is
			// already gone, treat the uninstall as complete (idempotent);
			// otherwise classify the failure below so the user gets guidance
			// that matches the actual cause.
			status, detectErr := i.detector.DetectTool(ctx, tool)
			if detectErr == nil && !status.Installed {
				result.Success = true
				result.Duration = time.Since(start)
				return result, nil
			}

			// VS Code refuses to remove an extension that other installed
			// extensions depend on (and `--force` does not override this).
			// Surface accurate, actionable guidance instead of the generic
			// "no record" message, which would misattribute the cause.
			if detail, ok := vscodeDependencyConflict(strategy.PackageManager, res.Stderr); ok {
				result.Error = i.vscodeDependencyUninstallError(tool, detail)
				result.Duration = time.Since(start)
				return result, nil
			}

			// The package manager no longer has a record of the package
			// (e.g. a self-updating CLI replaced the manager-installed copy
			// via its own `update` command). Only this signature warrants
			// the "updated outside the package manager" guidance.
			if packageManagerLostRecord(strategy.PackageManager, res) {
				// Echo the manager's own message when it has one; some
				// managers signal the lost-record case via an exit code with
				// little or no output, so fall back to the command error to
				// avoid a blank/noisy message.
				reported := strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
				if reported == "" {
					reported = err.Error()
				}
				result.Error = i.packageManagerUninstallFailedError(tool, strategy, reported)
				result.Duration = time.Since(start)
				return result, nil
			}

			// Any other package-manager failure (permissions, locks,
			// network, manager-internal errors, etc.): return the actual
			// error directly without speculating about the cause.
			if res.Stderr != "" {
				result.Error = fmt.Errorf(
					"uninstalling %s with %s failed: %s",
					tool.Name, strategy.PackageManager, res.Stderr,
				)
			} else {
				result.Error = fmt.Errorf(
					"running uninstall command for %s: %w", tool.Name, err,
				)
			}
			result.Duration = time.Since(start)
			return result, nil
		}
	} else {
		// Tools installed via a custom shell command with no reverse
		// command (and no package manager) have no automated uninstall.
		result.Strategy = "manual"
		result.Error = i.uninstallUnsupportedError(tool, strategy)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 4. Verify removal by detecting the tool again. Non-CLI tools may
	//    need a brief delay before the package manager stops reporting
	//    them, so we retry with exponential backoff.
	maxAttempts := 1
	if tool.Category != ToolCategoryCLI {
		maxAttempts = 4 // 1 initial + 3 retries
	}

	var status *ToolStatus
	gone, verifyErr := i.retryDetect(ctx, maxAttempts, tool.Name, func() (bool, error) {
		var e error
		status, e = i.detector.DetectTool(ctx, tool)
		if e != nil {
			return false, e
		}
		return !status.Installed, nil
	})
	if verifyErr != nil {
		result.Error = fmt.Errorf(
			"verifying removal of %s: %w", tool.Name, verifyErr,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	if !gone {
		result.Error = fmt.Errorf(
			"%s uninstall ran but the tool is still detected", tool.Name,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 5. Success — the tool is no longer detected.
	result.Success = true
	result.Duration = time.Since(start)
	return result, nil
}

// detectInstallingStrategy returns the strategy whose package manager currently
// has a record of the tool installed, or nil when no package-manager strategy
// owns it. Strategies are checked in list order, so the preferred manager wins
// when several report the tool. A nil result combined with the tool still being
// detected on PATH indicates a self-contained install (e.g. the install
// script) that no package manager tracks.
func (i *installer) detectInstallingStrategy(
	ctx context.Context,
	strategies []InstallStrategy,
	platform *Platform,
) *InstallStrategy {
	for idx := range strategies {
		s := &strategies[idx]
		if s.PackageManager == "" || s.PackageId == "" {
			continue
		}
		if !platform.HasManager(s.PackageManager) {
			continue
		}
		if i.managerHasPackage(ctx, s) {
			return s
		}
	}
	return nil
}

// managerHasPackage reports whether the strategy's package manager currently
// has a record of the package installed. Each manager exposes a different
// "list" command, but all exit non-zero when the package is absent. For npm
// and winget the package id must also appear in the output as a guard against
// partial matches; brew's list prints artifact paths, so its exit code alone
// is used.
func (i *installer) managerHasPackage(
	ctx context.Context,
	s *InstallStrategy,
) bool {
	switch s.PackageManager {
	case "npm":
		res, err := i.commandRunner.Run(ctx, exec.NewRunArgs(
			"npm", "ls", "-g", s.PackageId, "--depth=0",
		))
		return err == nil && strings.Contains(res.Stdout, s.PackageId)
	case "brew":
		args := []string{"list"}
		if s.Cask {
			args = append(args, "--cask")
		}
		args = append(args, s.PackageId)
		_, err := i.commandRunner.Run(ctx, exec.NewRunArgs("brew", args...))
		return err == nil
	case "winget":
		res, err := i.commandRunner.Run(ctx, exec.NewRunArgs(
			"winget", "list", "--id", s.PackageId, "-e",
			"--accept-source-agreements",
		))
		return err == nil && strings.Contains(res.Stdout, s.PackageId)
	default:
		return false
	}
}

// uninstallWithoutManagerOwner handles a multi-method tool that no package
// manager has a record of. When the tool is already gone the uninstall is
// idempotently successful; otherwise it was installed by a self-contained
// method (install script / direct download), so azd guides the user to remove
// the detected binary.
func (i *installer) uninstallWithoutManagerOwner(
	ctx context.Context,
	tool *ToolDefinition,
	start time.Time,
) (*InstallResult, error) {
	result := &InstallResult{Tool: tool, Strategy: "manual"}

	if status, err := i.detector.DetectTool(ctx, tool); err == nil &&
		!status.Installed {
		result.Success = true
		result.Duration = time.Since(start)
		return result, nil
	}

	result.Error = i.binaryRemovalUninstallError(tool)
	result.Duration = time.Since(start)
	return result, nil
}

// binaryRemovalUninstallError builds an [errorhandler.ErrorWithSuggestion] for
// a tool that is present but not tracked by any package manager azd can use
// (installed via the install script or a direct download). It names the actual
// on-PATH binary and the command to remove it.
func (i *installer) binaryRemovalUninstallError(tool *ToolDefinition) error {
	binaryPath := ""
	if tool.DetectCommand != "" && i.lookPath != nil {
		if resolved, err := i.lookPath(tool.DetectCommand); err == nil {
			binaryPath = resolved
		}
	}

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"%s is not tracked by a package manager on this system", tool.Name,
		),
		Message:    "Cannot uninstall " + tool.Name,
		Suggestion: selfManagedRemovalSuggestion(tool, binaryPath),
	}
}

// selfManagedRemovalSuggestion builds manual-removal guidance for a binary not
// tracked by a package manager. When binaryPath is non-empty it names the exact
// file and the command to remove it (with sudo for system locations).
func selfManagedRemovalSuggestion(tool *ToolDefinition, binaryPath string) string {
	intro := fmt.Sprintf(
		"%s was installed outside a package manager azd can use on this "+
			"platform (for example via the install script or a direct "+
			"download).",
		tool.Name,
	)

	if binaryPath == "" {
		return fmt.Sprintf(
			"%s Remove the %q binary from your PATH manually.",
			intro, tool.DetectCommand,
		)
	}

	return fmt.Sprintf(
		"%s Remove it manually:\n\n%s",
		intro, removeCommandFor(binaryPath),
	)
}

// removeCommandFor returns the OS-appropriate command to delete the file at
// binaryPath, prefixing sudo for system locations on Unix.
func removeCommandFor(binaryPath string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Remove-Item '%s'", binaryPath)
	}
	if isSystemBinaryPath(binaryPath) {
		return fmt.Sprintf("sudo rm %q", binaryPath)
	}
	return fmt.Sprintf("rm %q", binaryPath)
}

// isSystemBinaryPath reports whether binaryPath lives in a system-owned
// location that typically requires elevated privileges to modify.
func isSystemBinaryPath(binaryPath string) bool {
	systemPrefixes := []string{
		"/usr/", "/opt/", "/bin/", "/sbin/", "/Library/",
	}
	for _, prefix := range systemPrefixes {
		if strings.HasPrefix(binaryPath, prefix) {
			return true
		}
	}
	return false
}

// runSkillUninstall removes a skill from the resolved target agents and
// verifies, per agent, that the skill is no longer present. It mirrors
// runSkill but uses each agent's PluginUninstallCommand and inverts the
// verification (success means the plugin is gone).
func (i *installer) runSkillUninstall(
	ctx context.Context,
	tool *ToolDefinition,
	agents []string,
	renderer StepRenderer,
	stdin io.Reader,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	if len(tool.SkillAgents) == 0 {
		result.Error = fmt.Errorf("%s has no SkillAgents configured", tool.Name)
		result.Duration = time.Since(start)
		return result, nil
	}

	targets, err := i.resolveSkillUninstallTargets(ctx, tool, agents)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result, nil
	}

	var (
		succeeded []string
		failures  []error
	)
	for _, agent := range targets {
		title := fmt.Sprintf("Uninstalling %s from %s", tool.Name, agent.DisplayName)
		agentErr := renderSkillStep(ctx, renderer, title, true, func(out io.Writer) (string, error) {
			return "", i.uninstallSkillForAgent(ctx, tool, agent, out, stdin)
		})
		if agentErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", agent.DisplayName, agentErr))
			continue
		}
		succeeded = append(succeeded, agent.Command)
	}

	result.Strategy = strings.Join(succeeded, ", ")
	result.Duration = time.Since(start)

	// On failure, preserve the wrapped error for a single agent so callers
	// can match it with errors.Is/As; summarize when several agents fail.
	if len(failures) > 0 {
		if len(failures) == 1 {
			result.Error = failures[0]
		} else {
			msgs := make([]string, len(failures))
			for j, f := range failures {
				msgs[j] = f.Error()
			}
			result.Error = fmt.Errorf(
				"%s could not be uninstalled for %d agent(s): %s",
				tool.Name, len(failures), strings.Join(msgs, "; "),
			)
		}
		return result, nil
	}

	result.Success = true
	return result, nil
}

// resolveSkillUninstallTargets resolves the agents a skill should be
// removed from. With explicit agent names, each must be a configured
// SkillAgent that is a usable (functional) CLI on PATH. Without explicit
// agents (the default, and also `--agent all`), it targets every agent the
// skill is currently installed through; an error is returned when the
// skill is not installed on any agent.
func (i *installer) resolveSkillUninstallTargets(
	ctx context.Context,
	tool *ToolDefinition,
	agents []string,
) ([]SkillAgent, error) {
	if len(agents) > 0 {
		return i.explicitSkillAgentTargets(ctx, tool, agents)
	}

	// Default / --agent all: remove from every agent the skill is
	// currently installed through.
	installed, err := i.detector.DetectSkillAgents(ctx, tool)
	if err != nil {
		return nil, err
	}

	targets := configuredSkillAgentsFor(tool, installed)

	if len(targets) == 0 {
		// Nothing to do. No Links: the suggestion is self-contained.
		return nil, &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"%s is not installed on any available agent", tool.Name,
			),
			Message: "Cannot uninstall " + tool.Name,
			Suggestion: fmt.Sprintf(
				"%s is not installed through any agent, so there is "+
					"nothing to uninstall.", tool.Name,
			),
		}
	}
	return targets, nil
}

// uninstallSkillForAgent removes the skill through a single agent and
// verifies it is no longer present on that agent. out, when non-nil, receives
// the agent CLI's output line-by-line for display above the step spinner.
func (i *installer) uninstallSkillForAgent(
	ctx context.Context,
	tool *ToolDefinition,
	agent SkillAgent,
	out io.Writer,
	stdin io.Reader,
) error {
	if err := i.runSkillAgentUninstallCommand(ctx, agent, out, stdin); err != nil {
		return err
	}
	return i.verifySkillUninstalled(ctx, tool, agent)
}

// runSkillAgentUninstallCommand executes the agent's plugin-uninstall command.
// When a step spinner is showing (out is non-nil) the agent CLI's output is
// routed through out so any prompt is printed above the spinner and
// answerable via the connected stdin; otherwise the command runs fully
// interactively (see skillCommandRunArgs). A non-zero exit is returned as an
// error; the caller verifies via the detector and decides whether the error
// is fatal.
func (i *installer) runSkillAgentUninstallCommand(
	ctx context.Context,
	agent SkillAgent,
	out io.Writer,
	stdin io.Reader,
) error {
	cmd := agent.PluginUninstallCommand
	if len(cmd) == 0 {
		return fmt.Errorf(
			"agent %q has no uninstall command configured", agent.DisplayName,
		)
	}

	runArgs := skillCommandRunArgs(exec.NewRunArgs(agent.Command, cmd...), out, stdin)
	if _, err := i.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf(
			"running `%s %s`: %w",
			agent.Command, strings.Join(cmd, " "), err,
		)
	}

	return nil
}

// verifySkillUninstalled confirms the skill is no longer detectable
// through the specific agent it was removed via. Like verifySkillInstalled
// it is agent-scoped and retries with backoff because plugin-list output
// can lag the uninstall action.
func (i *installer) verifySkillUninstalled(
	ctx context.Context,
	tool *ToolDefinition,
	agent SkillAgent,
) error {
	const maxAttempts = 4 // 1 initial + 3 retries

	gone, err := i.retryDetect(ctx, maxAttempts, tool.Name, func() (bool, error) {
		installed, detectErr := i.detector.DetectSkillAgents(ctx, tool)
		if detectErr != nil {
			return false, detectErr
		}
		for _, h := range installed {
			if h.Agent == agent.Command {
				return false, nil // still installed on this agent
			}
		}
		return true, nil // no longer installed via this agent
	})
	if err != nil {
		return fmt.Errorf("verifying removal of %s: %w", tool.Name, err)
	}
	if !gone {
		return fmt.Errorf(
			"%s was uninstalled via %s but verification failed",
			tool.Name, agent.DisplayName,
		)
	}
	return nil
}

// run is the shared implementation for Install and Upgrade.
func (i *installer) run(
	ctx context.Context,
	tool *ToolDefinition,
	upgrade bool,
	opts []InstallOption,
) (*InstallResult, error) {
	var cfg installConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Skills follow a different flow: they install through the agent CLI
	// native plugin command rather than the platform's
	// package manager, so we short-circuit before platform detection.
	if tool.Category == ToolCategorySkill {
		return i.runSkill(ctx, tool, upgrade, cfg.agents, cfg.allAvailableAgents, cfg.renderer, cfg.stdin)
	}

	// Non-skill tools install as a single step through the platform
	// package manager; render one spinner for the tool (no agent) when a
	// renderer is supplied.
	if cfg.renderer == nil {
		return i.runToolInstall(ctx, tool, upgrade)
	}
	verb := "Installing"
	if upgrade {
		verb = "Upgrading"
	}
	title := fmt.Sprintf("%s %s", verb, tool.Name)
	cfg.renderer.ShowSpinner(ctx, title, input.Step)
	result, err := i.runToolInstall(ctx, tool, upgrade)
	// On a successful upgrade, append the resulting version to the result
	// line, mirroring skills — e.g. "Upgrading Azure CLI (v2.0.0)".
	doneTitle := title
	if upgrade && err == nil && result != nil && result.Success && result.InstalledVersion != "" {
		doneTitle = fmt.Sprintf("%s (v%s)", title, result.InstalledVersion)
	}
	cfg.renderer.StopSpinner(ctx, doneTitle, input.GetStepResultFormat(stepError(result, err)))
	return result, err
}

// runToolInstall installs or upgrades a non-skill tool through the platform
// package manager (or a direct download) and verifies the result.
func (i *installer) runToolInstall(
	ctx context.Context,
	tool *ToolDefinition,
	upgrade bool,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	// 1. Detect the platform (cached after the first call).
	platform, err := i.ensurePlatform(ctx)
	if err != nil {
		result.Error = fmt.Errorf(
			"detecting platform: %w", err,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Select the best install strategy for this platform.
	strategy := i.platformDetector.SelectStrategy(
		tool, platform,
	)
	if strategy == nil {
		result.Error = fmt.Errorf(
			"no install strategy for %s on platform %s",
			tool.Name, platform.OS,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Determine a human-readable label for the strategy.
	strategyLabel := strategy.PackageManager
	if strategyLabel == "" {
		strategyLabel = "command"
	}
	result.Strategy = strategyLabel

	// 3. When the strategy names a package manager but has no
	//    explicit InstallCommand, verify the manager is available.
	if strategy.PackageManager != "" &&
		strategy.InstallCommand == "" &&
		strategy.DirectDownloadUrl == "" &&
		!platform.HasManager(strategy.PackageManager) {
		result.Strategy = "manual"
		result.Error = i.managerUnavailableError(
			tool, strategy,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 4. Execute the install using the best available mechanism.
	if strategy.DirectDownloadUrl != "" {
		// Direct download: azd manages the download, checksum,
		// and placement.
		if err := i.executeDirectDownload(
			ctx, strategy,
		); err != nil {
			result.Error = fmt.Errorf(
				"direct download for %s: %w",
				tool.Name, err,
			)
			result.Duration = time.Since(start)
			return result, nil
		}
		result.Strategy = "direct-download"
	} else if err := i.executeStrategy(
		ctx, strategy, upgrade,
	); err != nil {
		result.Error = fmt.Errorf(
			"running install command for %s: %w",
			tool.Name, err,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 5. Verify installation by detecting the tool again.
	//    Non-CLI tools (extensions, servers, libraries) may need
	//    a brief delay before the package manager reports the new
	//    version, so we retry with exponential backoff.
	maxAttempts := 1
	if tool.Category != ToolCategoryCLI {
		maxAttempts = 4 // 1 initial + 3 retries
	}

	var status *ToolStatus
	found, verifyErr := i.retryDetect(ctx, maxAttempts, tool.Name, func() (bool, error) {
		var e error
		status, e = i.detector.DetectTool(ctx, tool)
		if e != nil {
			return false, e
		}
		return status.Installed, nil
	})
	if verifyErr != nil {
		result.Error = fmt.Errorf(
			"verifying installation of %s: %w", tool.Name, verifyErr,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	if !found {
		result.Error = fmt.Errorf(
			"%s was installed but verification failed",
			tool.Name,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 6. Success — record the detected version and duration.
	result.Success = true
	result.InstalledVersion = status.InstalledVersion
	result.Duration = time.Since(start)

	return result, nil
}

// retryDetect repeats detect with exponential backoff (starting at
// i.retryBackoff, doubling each attempt) until it reports found=true or
// maxAttempts is exhausted. label names the tool in retry logs. It
// returns found=true on success; a non-nil error only for a detect
// failure or context cancellation. Plugin/package listings sometimes lag
// the install action, so both the package-manager and skill install
// paths share this helper to converge on detection.
func (i *installer) retryDetect(
	ctx context.Context,
	maxAttempts int,
	toolName string,
	detect func() (bool, error),
) (bool, error) {
	backoff := i.retryBackoff
	for attempt := range maxAttempts {
		found, err := detect()
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}

		// No more retries left — report not-found.
		if attempt >= maxAttempts-1 {
			break
		}

		log.Printf(
			"installer: %s not yet detected, retrying in %s (attempt %d/%d)",
			toolName, backoff, attempt+1, maxAttempts-1,
		)

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// Skill install / upgrade
// ---------------------------------------------------------------------------

// runSkill installs (or upgrades) a skill across one or more agentic CLI
// agents.
//
// Prerequisite rules:
//  1. HARD — at least one supported agent CLI (copilot or claude)
//     must be on PATH. We do NOT install one ourselves; if none is
//     present resolveSkillTargets fails with an
//     [errorhandler.ErrorWithSuggestion] pointing at the install docs.
//  2. SOFT — Node.js (`node`) on PATH. The Azure MCP server is started
//     via `npx`, so its absence breaks MCP-backed scenarios but does NOT
//     prevent installing the skill files. We warn and continue.
//  3. Git is NOT pre-checked. The agent CLI fetches the skill repo itself
//     and surfaces its own diagnostic when git is missing.
//
// The agents argument, when non-empty, restricts the operation to the
// named agents. When allAvailable is true (and agents is empty) the skill
// is installed through every configured agent on PATH. Otherwise the
// single preferred agent (first on PATH) is used (or, for an upgrade,
// every agent the skill is already installed through).
func (i *installer) runSkill(
	ctx context.Context,
	tool *ToolDefinition,
	upgrade bool,
	agents []string,
	allAvailable bool,
	renderer StepRenderer,
	stdin io.Reader,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	if len(tool.SkillAgents) == 0 {
		result.Error = fmt.Errorf("%s has no SkillAgents configured", tool.Name)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 1. HARD prerequisite: resolve the target agent(s).
	targets, err := i.resolveSkillTargets(ctx, tool, agents, allAvailable, upgrade)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. SOFT prerequisite: warn if Node.js is missing. The Azure MCP
	//    server is started via `npx`, so its absence breaks MCP-backed
	//    scenarios but does not prevent installing the skill files. Write
	//    to stderr so structured stdout stays clean in `--output json`.
	if err := i.commandRunner.ToolInPath("node"); err != nil {
		log.Printf("node not found on PATH: %v", err)
		fmt.Fprintln(os.Stderr, output.WithWarningFormat(
			"WARNING: node not found on PATH; %s "+
				"requires Node.js to run fully to start the MCP servers. "+
				"Please install Node.js: ",
			tool.Name,
		)+output.WithLinkFormat("https://nodejs.org/"))
	}

	// 3. Install / upgrade for each target agent, collecting outcomes.
	//    renderSkillStep shows a step spinner per agent. For an install the
	//    agent CLI's output streams above the spinner (so prompts are
	//    answerable); for an upgrade the upgrade command's output is captured
	//    and parsed for the version and whether the skill was already at the
	//    latest, which the result line reports.
	verb := "Installing"
	if upgrade {
		verb = "Upgrading"
	}
	var (
		succeeded   []string
		failures    []error
		version     string
		anyUpgraded bool // an upgrade actually changed at least one agent
	)
	for _, agent := range targets {
		title := fmt.Sprintf("%s %s in %s", verb, tool.Name, agent.DisplayName)
		var (
			agentVersion  string
			agentUpToDate bool
		)
		agentErr := renderSkillStep(ctx, renderer, title, true, func(out io.Writer) (string, error) {
			v, upToDate, e := i.installSkillForAgent(ctx, tool, agent, upgrade, out, stdin)
			agentVersion = v
			agentUpToDate = upToDate
			if e != nil {
				return "", e
			}
			// Result line: for an upgrade, report the version and whether the
			// skill was already current; otherwise reuse the step title.
			done := title
			switch {
			case upgrade && upToDate && v != "":
				done = fmt.Sprintf("%s in %s is already up to date (v%s).", tool.Name, agent.DisplayName, v)
			case upgrade && upToDate:
				done = fmt.Sprintf("%s in %s is already up to date.", tool.Name, agent.DisplayName)
			case upgrade && v != "":
				done = fmt.Sprintf("%s (v%s)", title, v)
			}
			return done, nil
		})
		if agentErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", agent.DisplayName, agentErr))
			continue
		}
		succeeded = append(succeeded, agent.Command)
		if !agentUpToDate {
			anyUpgraded = true
		}
		if version == "" {
			version = agentVersion
		}
	}

	// An upgrade that changed nothing (every targeted agent was already at the
	// latest version) is reported as "already up to date" by the caller.
	result.AlreadyUpToDate = upgrade && len(succeeded) > 0 && !anyUpgraded

	result.Strategy = strings.Join(succeeded, ", ")
	result.InstalledVersion = version
	result.Duration = time.Since(start)

	// On failure, preserve the wrapped error for a single agent so callers
	// can match it with errors.Is/As; summarize when several agents fail.
	if len(failures) > 0 {
		if len(failures) == 1 {
			result.Error = failures[0]
		} else {
			msgs := make([]string, len(failures))
			for j, f := range failures {
				msgs[j] = f.Error()
			}
			result.Error = fmt.Errorf(
				"%s could not be installed for %d agent(s): %s",
				tool.Name, len(failures), strings.Join(msgs, "; "),
			)
		}
		return result, nil
	}

	result.Success = true
	return result, nil
}

// resolveSkillTargets resolves the agents a skill should be installed
// to. With an explicit selection (agents) every named agent must be a
// configured SkillAgent that is on PATH; otherwise an error naming the
// available agents is returned. With allAvailable it acts on every
// configured agent on PATH: for install, all of them; for upgrade, only
// the ones that already have the skill installed (the rest are skipped
// with a warning, and an error is returned when none have it). With
// neither it returns a single agent for install (the preferred agent on
// PATH) or, for an upgrade, every agent the skill is already installed
// through.
func (i *installer) resolveSkillTargets(
	ctx context.Context,
	tool *ToolDefinition,
	agents []string,
	allAvailable bool,
	upgrade bool,
) ([]SkillAgent, error) {
	if len(agents) == 0 {
		// Batch / --agent all: act on every configured agent on PATH,
		// resolved here (at run time) so agent CLIs installed earlier in
		// the same batch are picked up.
		if allAvailable {
			var onPath []SkillAgent
			for _, agent := range tool.SkillAgents {
				if i.agentUsable(ctx, agent) {
					onPath = append(onPath, agent)
				}
			}
			// No usable agent CLI present at all — surface the install
			// guidance.
			if len(onPath) == 0 {
				agent, err := i.pickSkillAgent(ctx, tool)
				if err != nil {
					return nil, err
				}
				return []SkillAgent{agent}, nil
			}

			// For install, target every agent on PATH.
			if !upgrade {
				return onPath, nil
			}

			// For upgrade, target only agents that actually have the skill
			// installed; warn-and-skip the rest, since an agent CLI cannot
			// upgrade a plugin it never installed.
			installed, err := i.detector.DetectSkillAgents(ctx, tool)
			if err != nil {
				return nil, err
			}
			installedSet := map[string]bool{}
			for _, h := range installed {
				installedSet[h.Agent] = true
			}
			var targets []SkillAgent
			for _, agent := range onPath {
				if installedSet[agent.Command] {
					targets = append(targets, agent)
					continue
				}
				fmt.Fprintln(os.Stderr, output.WithWarningFormat(
					"Skipping upgrade for %s: %s is not installed on it.",
					agent.DisplayName, tool.Name,
				))
			}
			if len(targets) == 0 {
				onPathNames := make([]string, len(onPath))
				for j, h := range onPath {
					onPathNames[j] = h.DisplayName
				}
				return nil, fmt.Errorf(
					"%s is not installed on any available agent (%s); "+
						"nothing to upgrade",
					tool.Name, strings.Join(onPathNames, ", "),
				)
			}
			return targets, nil
		}

		// For an upgrade with no explicit agent, refresh every agent the
		// skill is currently installed through — not just the first —
		// so a multi-agent install (e.g. copilot AND claude) is kept
		// fully up to date. We also avoid running an upgrade against a
		// agent that never installed it.
		if upgrade {
			installed, err := i.detector.DetectSkillAgents(ctx, tool)
			if err != nil {
				return nil, err
			}

			if len(installed) > 0 {
				if targets := configuredSkillAgentsFor(tool, installed); len(targets) > 0 {
					return targets, nil
				}
			}

			// The skill is not installed on any available agent. Don't
			// fall through to pickSkillAgent — updating a plugin that was
			// never installed only produces a confusing "verification
			// failed" error. Point the user at install instead. No Links:
			// the suggestion is a self-contained azd command, so there is
			// nothing to link (cf. managerUnavailableError).
			return nil, &errorhandler.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"%s is not installed on any available agent",
					tool.Name,
				),
				Message: "Cannot upgrade " + tool.Name,
				Suggestion: fmt.Sprintf(
					"%s is not installed yet. Install it first:\n\n"+
						"    azd tool install %s",
					tool.Name, tool.Id,
				),
			}
		}
		agent, err := i.pickSkillAgent(ctx, tool)
		if err != nil {
			return nil, err
		}
		return []SkillAgent{agent}, nil
	}

	return i.explicitSkillAgentTargets(ctx, tool, agents)
}

// explicitSkillAgentTargets resolves an explicit list of requested agent
// names to their [SkillAgent] definitions. A requested agent is usable only
// when it is a configured SkillAgent that is also a functional CLI on PATH
// (see [installer.agentUsable]); an unknown name, an agent not on PATH, and a
// agent present only as a launcher stub all fail with an error naming the
// supported agents. Shared by the install/upgrade (resolveSkillTargets) and
// uninstall (resolveSkillUninstallTargets) paths so the agent-availability
// rule lives in one place.
func (i *installer) explicitSkillAgentTargets(
	ctx context.Context,
	tool *ToolDefinition,
	agents []string,
) ([]SkillAgent, error) {
	targets := make([]SkillAgent, 0, len(agents))
	for _, name := range agents {
		agent, ok := findSkillAgent(tool, name)
		if !ok || !i.agentUsable(ctx, agent) {
			supported := make([]string, len(tool.SkillAgents))
			for j, h := range tool.SkillAgents {
				supported[j] = h.Command
			}
			return nil, fmt.Errorf(
				"agent %q is not available for %s; supported agents: %s",
				name, tool.Name, strings.Join(supported, ", "),
			)
		}
		targets = append(targets, agent)
	}
	return targets, nil
}

// findSkillAgent returns the configured SkillAgent whose command identity
// matches name (case-insensitively) and whether one was found. Matching is by
// Command only (e.g. "copilot"), never the display Agent: --agent values are
// command names, and the interactive picker maps its display selection back
// to the command before resolving here. It centralizes the SkillAgents lookup
// shared by the skill install/upgrade and uninstall paths.
func findSkillAgent(tool *ToolDefinition, name string) (SkillAgent, bool) {
	idx := slices.IndexFunc(tool.SkillAgents, func(h SkillAgent) bool {
		return strings.EqualFold(h.Command, name)
	})
	if idx < 0 {
		return SkillAgent{}, false
	}
	return tool.SkillAgents[idx], true
}

// configuredSkillAgentsFor maps a set of installed agents back to their
// configured SkillAgent definitions, in installed order, skipping any agent that
// is no longer a configured SkillAgent. Shared by the upgrade
// (resolveSkillTargets) and uninstall (resolveSkillUninstallTargets) paths,
// which both act on "the agents the skill is currently installed through".
func configuredSkillAgentsFor(tool *ToolDefinition, installed []InstalledSkillAgent) []SkillAgent {
	targets := make([]SkillAgent, 0, len(installed))
	for _, inst := range installed {
		if agent, ok := findSkillAgent(tool, inst.Agent); ok {
			targets = append(targets, agent)
		}
	}
	return targets
}

// agentUsable reports whether an agent CLI on PATH is a functional
// CLI rather than a same-named launcher stub.
//
// Some environments put a stub on PATH — notably the VS Code Copilot Chat
// extension, whose `copilot` stub only prints "Install GitHub Copilot CLI?"
// and exits 0. It passes a bare existence check but cannot install the skill,
// which used to surface as a misleading "verification failed".
//
// To tell them apart, agentUsable runs the agent's version command (with empty
// stdin so a prompting stub reads EOF and exits) and accepts the agent only
// when the output matches its BinaryVersionRegex, anchored to the agent's
// `--version` banner. Agents without a version probe fall back to the
// existence check.
//
// Results are memoized per agent (agentProbe); only on-PATH agents are cached,
// so an agent installed earlier in the same batch is still picked up. The cache
// assumes an on-PATH agent binary is not swapped mid-command, which azd never
// does.
func (i *installer) agentUsable(ctx context.Context, agent SkillAgent) bool {
	if i.commandRunner.ToolInPath(agent.Command) != nil {
		return false
	}

	i.agentProbeMu.Lock()
	if i.agentProbe == nil {
		i.agentProbe = map[string]bool{}
	}
	usable, ok := i.agentProbe[agent.Command]
	i.agentProbeMu.Unlock()
	if ok {
		return usable
	}

	usable = i.probeOnPathAgent(ctx, agent)

	i.agentProbeMu.Lock()
	i.agentProbe[agent.Command] = usable
	i.agentProbeMu.Unlock()
	return usable
}

// probeOnPathAgent runs the version probe for an agent already confirmed to be on
// PATH and reports whether it is a functional CLI. It is the uncached half of
// agentUsable; see that method for the rationale behind the matching.
func (i *installer) probeOnPathAgent(ctx context.Context, agent SkillAgent) bool {
	if len(agent.BinaryVersionArgs) == 0 || agent.BinaryVersionRegex == "" {
		return true
	}
	result, err := i.commandRunner.Run(
		ctx,
		exec.NewRunArgs(agent.Command, agent.BinaryVersionArgs...).
			WithStdIn(strings.NewReader("")),
	)
	// A cancelled/timed-out probe is not evidence the agent is a stub; do
	// not penalize it here (context handling is the caller's concern).
	if isContextErr(err) {
		return true
	}
	return matchVersion(result.Stdout+"\n"+result.Stderr, agent.BinaryVersionRegex) != ""
}

// pickSkillAgent returns the first SkillAgent that is a usable (functional)
// CLI — see [installer.agentUsable], which rejects launcher stubs that merely
// share the agent's name on PATH. When none of the configured agents is usable
// it returns an [errorhandler.ErrorWithSuggestion] (all four fields populated
// per the AGENTS.md completeness rule) that recommends installing GitHub
// Copilot CLI via `azd tool install github-copilot-cli` — a single command
// the user can copy-paste without leaving azd.
func (i *installer) pickSkillAgent(
	ctx context.Context,
	tool *ToolDefinition,
) (SkillAgent, error) {
	var checked []string
	for _, agent := range tool.SkillAgents {
		if i.agentUsable(ctx, agent) {
			return agent, nil
		}
		checked = append(checked, agent.Command)
	}

	suggestion := fmt.Sprintf(
		"%s installs through your existing agentic CLI. Install GitHub "+
			"Copilot CLI:\n\n"+
			"    azd tool install github-copilot-cli\n\n"+
			"Then re-run `azd tool install %s`.\n"+
			"Checked (none found on PATH): %s",
		tool.Name, tool.Id, strings.Join(checked, ", "),
	)

	return SkillAgent{}, &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"no supported agent CLI found on PATH for %s",
			tool.Name,
		),
		Message:    "Cannot install " + tool.Name,
		Suggestion: suggestion,
		Links: []errorhandler.ErrorLink{
			{
				URL:   "https://docs.github.com/copilot/how-tos/set-up/install-copilot-cli",
				Title: "Install GitHub Copilot CLI",
			},
		},
	}
}

// installSkillForAgent installs (or upgrades) the skill through a single agent
// and verifies the result. It returns the version and, for an upgrade,
// whether the agent reported the skill was already at the latest version. For
// an upgrade the version comes from the upgrade command's output (falling back
// to the detected version); for an install it comes from post-install
// detection. out, when non-nil, receives an install's streamed agent output
// for display above the step spinner.
func (i *installer) installSkillForAgent(
	ctx context.Context,
	tool *ToolDefinition,
	agent SkillAgent,
	upgrade bool,
	out io.Writer,
	stdin io.Reader,
) (version string, alreadyLatest bool, err error) {
	// For an upgrade, record the agent's installed version before updating so
	// "already up to date" is decided by comparing the actual version before
	// and after — not by parsing the agent CLI's prose, which varies by agent
	// and misfires when the wording is not recognized.
	var beforeVersion string
	if upgrade {
		if installed, detectErr := i.detector.DetectSkillAgents(ctx, tool); detectErr == nil {
			beforeVersion, _ = installedAgentVersion(installed, agent.Command)
		}
	}

	cmdOutput, err := i.runSkillAgentCommand(ctx, agent, upgrade, out, stdin)
	if err != nil {
		return "", false, err
	}
	var proseLatest bool
	if upgrade {
		version, proseLatest = parseUpgradeOutput(cmdOutput)
	}

	detectedVersion, err := i.verifySkillInstalled(ctx, tool, agent)
	if err != nil {
		return "", false, err
	}
	// Prefer the version the upgrade command reported; fall back to the
	// version detected via the plugin list.
	if version == "" {
		version = detectedVersion
	}

	if upgrade {
		// The authoritative "already up to date" signal is an unchanged
		// version. Only when a version is unavailable on either side fall back
		// to the agent CLI's prose, so azd neither claims up to date without
		// evidence nor misreports an upgrade when the wording is unrecognized.
		if beforeVersion != "" && detectedVersion != "" {
			alreadyLatest = beforeVersion == detectedVersion
		} else {
			alreadyLatest = proseLatest
		}
	}

	return version, alreadyLatest, nil
}

// installedAgentVersion returns the installed version of the skill for the given
// agent command from a DetectSkillAgents result, and whether that agent was
// found. InstalledSkillAgent.Agent carries the executable identity, so the match
// is against agent.Command. (Distinct from detector.skillAgentVersion, which
// probes the agent CLI; this only looks up an already-detected list.)
func installedAgentVersion(installed []InstalledSkillAgent, command string) (string, bool) {
	for _, h := range installed {
		if h.Agent == command {
			return h.Version, true
		}
	}
	return "", false
}

// verifySkillInstalled confirms the skill is detectable **through the
// specific agent** it was just installed via, and returns that agent's
// version. This is agent-scoped on purpose: verifying via the generic
// DetectTool would report success whenever ANY agent has the skill, so a
// silent no-op install on a secondary agent (e.g. `--agent claude` while
// copilot already has it) would be falsely reported as success with the
// wrong agent's version. Plugin-list output sometimes lags the install
// action (the pre-existing copilot CLI integration documents the same
// race — see internal/agent/copilot/cli.go), so it retries a few times
// with exponential backoff.
func (i *installer) verifySkillInstalled(
	ctx context.Context,
	tool *ToolDefinition,
	agent SkillAgent,
) (string, error) {
	const maxAttempts = 4 // 1 initial + 3 retries
	var version string

	found, err := i.retryDetect(ctx, maxAttempts, tool.Name, func() (bool, error) {
		installed, detectErr := i.detector.DetectSkillAgents(ctx, tool)
		if detectErr != nil {
			return false, detectErr
		}
		if v, ok := installedAgentVersion(installed, agent.Command); ok {
			version = v
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf(
			"verifying installation of %s: %w", tool.Name, err,
		)
	}
	if !found {
		return "", fmt.Errorf(
			"%s was installed via %s but verification failed",
			tool.Name, agent.DisplayName,
		)
	}
	return version, nil
}

// runSkillAgentCommand executes the agent's install or upgrade command and
// returns the command's stdout (empty for a streamed install).
//
// Install and upgrade connect to the terminal differently:
//   - Install streams the agent CLI's output through out (when a spinner is
//     showing) or runs fully interactively (out nil), with stdin connected so
//     the user answers any prompt (marketplace trust, install confirmation).
//     azd never pipes canned answers. Nothing is captured, so "" is returned.
//     For a fresh install it first runs MarketplaceAddCommand when the agent
//     declares one.
//   - Upgrade captures the output (no streaming) and returns it so the caller
//     can parse the version and whether the skill was already at the latest.
//     The plugin is already installed (marketplace trusted), so the upgrade is
//     non-interactive.
//
// A non-zero exit is returned to the caller as an error; the caller is
// expected to verify via the detector and decide whether to treat the
// error as fatal (some agents return non-zero on idempotent re-install).
func (i *installer) runSkillAgentCommand(
	ctx context.Context,
	agent SkillAgent,
	upgrade bool,
	out io.Writer,
	stdin io.Reader,
) (string, error) {
	cmd := agent.PluginInstallCommand
	verb := "install"
	if upgrade {
		cmd = agent.PluginUpdateCommand
		verb = "upgrade"
	}
	if len(cmd) == 0 {
		return "", fmt.Errorf(
			"agent %q has no %s command configured", agent.DisplayName, verb,
		)
	}

	// Upgrade: capture the output so the caller can parse the version and the
	// "already at latest" state; do not stream it above the spinner.
	if upgrade {
		res, err := i.commandRunner.Run(ctx, exec.NewRunArgs(agent.Command, cmd...))
		if err != nil {
			return "", fmt.Errorf(
				"running `%s %s`: %w",
				agent.Command, strings.Join(cmd, " "), err,
			)
		}
		return res.Stdout, nil
	}

	if len(agent.MarketplaceAddCommand) > 0 {
		if err := i.runMarketplaceAdd(ctx, agent, out, stdin); err != nil {
			return "", err
		}
	}

	runArgs := skillCommandRunArgs(exec.NewRunArgs(agent.Command, cmd...), out, stdin)
	if _, err := i.commandRunner.Run(ctx, runArgs); err != nil {
		return "", fmt.Errorf(
			"running `%s %s`: %w",
			agent.Command, strings.Join(cmd, " "), err,
		)
	}

	return "", nil
}

// runMarketplaceAdd registers the skill marketplace with the agent CLI.
// out and stdin thread the step spinner's writer and the console's input
// through skillCommandRunArgs so the agent CLI's output prints above the
// spinner and any marketplace trust prompt stays visible and answerable
// while the spinner runs (matching the install phase). When out routes the
// output through a writer, CommandRunner still captures it (io.MultiWriter),
// so the captured stdout/stderr remains available for the already-added
// check below.
//
// Some agents (e.g. copilot) return a non-zero exit when the marketplace
// is already registered; we recognize that case from the captured
// output and treat it as success so the install can proceed. Agents that
// already exit 0 in the "already added" case (e.g. claude) flow
// through naturally. Any other failure is returned to the caller.
func (i *installer) runMarketplaceAdd(
	ctx context.Context,
	agent SkillAgent,
	out io.Writer,
	stdin io.Reader,
) error {
	args := skillCommandRunArgs(exec.NewRunArgs(agent.Command, agent.MarketplaceAddCommand...), out, stdin)
	result, err := i.commandRunner.Run(ctx, args)
	if err == nil {
		return nil
	}
	if isMarketplaceAlreadyAdded(result.Stdout + result.Stderr) {
		return nil
	}
	return fmt.Errorf(
		"running `%s %s`: %w",
		agent.Command, strings.Join(agent.MarketplaceAddCommand, " "), err,
	)
}

// isMarketplaceAlreadyAdded reports whether the agent CLI output indicates
// the marketplace is already registered. Observed wording per agent:
//   - copilot: "Failed to add marketplace: ... already registered"
//   - claude:  "Marketplace ... already on disk"
func isMarketplaceAlreadyAdded(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "already registered") ||
		strings.Contains(lower, "already added") ||
		strings.Contains(lower, "already on disk")
}

// ensurePlatform lazily detects the current platform using a mutex
// to guarantee thread-safe initialization. Only successful results
// are cached so that transient errors can be retried.
func (i *installer) ensurePlatform(
	ctx context.Context,
) (*Platform, error) {
	i.platformMu.Lock()
	defer i.platformMu.Unlock()
	if i.platform != nil {
		return i.platform, nil
	}
	p, err := i.platformDetector.Detect(ctx)
	if err != nil {
		return nil, fmt.Errorf("platform detection: %w", err)
	}
	i.platform = p
	return p, nil
}

// executeStrategy runs the command described by the given strategy.
// When upgrade is true the upgrade variant of the command is used
// where applicable. Commands containing shell operators (pipes,
// redirects, etc.) are executed through the system shell.
func (i *installer) executeStrategy(
	ctx context.Context,
	strategy *InstallStrategy,
	upgrade bool,
) error {
	// When the strategy has an explicit InstallCommand that uses
	// shell operators, delegate to the system shell directly so
	// that pipes and redirects work correctly (e.g.
	// "curl -sL ... | sudo bash").
	if strategy.InstallCommand != "" &&
		containsShellOperators(strategy.InstallCommand) {
		return i.executeShellCommand(ctx, strategy.InstallCommand)
	}

	cmd, args := i.buildCommand(strategy, upgrade)
	if cmd == "" {
		return fmt.Errorf("strategy produced an empty command")
	}

	runArgs := exec.NewRunArgs(cmd, args...)
	_, err := i.commandRunner.Run(ctx, runArgs)
	return err
}

// executeUninstall runs the package-manager uninstall command for the
// given strategy. It is only reached for strategies backed by a known
// package manager; direct-download and unsupported strategies are
// handled by runUninstall before this point.
//
// The full command result (stdout, stderr, exit code) is returned
// alongside any error so callers can inspect package-manager diagnostics —
// for example, VS Code's refusal to remove an extension that other
// extensions depend on (stderr), or winget reporting that it has no record
// of the package (stdout).
func (i *installer) executeUninstall(
	ctx context.Context,
	strategy *InstallStrategy,
) (exec.RunResult, error) {
	cmd, args := buildUninstallCommand(
		strategy.PackageManager, strategy.PackageId, strategy.Cask,
	)
	if cmd == "" {
		return exec.RunResult{}, fmt.Errorf("strategy produced an empty uninstall command")
	}

	runArgs := exec.NewRunArgs(cmd, args...)
	return i.commandRunner.Run(ctx, runArgs)
}

// executeUninstallCommand runs an explicit uninstall command string (the
// reverse of an InstallCommand, e.g. `azd extension uninstall <id>`).
// Commands containing shell operators are executed through the system
// shell; otherwise the command is split into an executable plus args.
func (i *installer) executeUninstallCommand(
	ctx context.Context,
	command string,
) error {
	if containsShellOperators(command) {
		return i.executeShellCommand(ctx, command)
	}

	cmd, args := splitCommand(command)
	if cmd == "" {
		return fmt.Errorf("empty uninstall command")
	}

	runArgs := exec.NewRunArgs(cmd, args...)
	_, err := i.commandRunner.Run(ctx, runArgs)
	return err
}

// buildCommand constructs the executable name and argument list for
// the given strategy. For upgrades the package-manager upgrade
// variant is preferred; when unavailable the install command is used
// as a fallback.
func (i *installer) buildCommand(
	strategy *InstallStrategy,
	upgrade bool,
) (string, []string) {
	// For upgrades, prefer the package-manager upgrade command
	// when both PackageManager and PackageId are available.
	if upgrade &&
		strategy.PackageManager != "" &&
		strategy.PackageId != "" {
		return buildManagerCommand(
			strategy.PackageManager,
			strategy.PackageId,
			true,
			strategy.Cask,
		)
	}

	// Use an explicit InstallCommand when present.
	if strategy.InstallCommand != "" {
		return splitCommand(strategy.InstallCommand)
	}

	// Fall back to package-manager install command.
	if strategy.PackageManager != "" &&
		strategy.PackageId != "" {
		return buildManagerCommand(
			strategy.PackageManager,
			strategy.PackageId,
			false,
			strategy.Cask,
		)
	}

	return "", nil
}

// managerUnavailableError builds an [errorhandler.ErrorWithSuggestion]
// for the case where the required package manager is not installed.
func (i *installer) managerUnavailableError(
	tool *ToolDefinition,
	strategy *InstallStrategy,
) error {
	suggestion := fmt.Sprintf(
		"Package manager %q is not available. "+
			"Install it first or install %s manually.",
		strategy.PackageManager, tool.Name,
	)

	var links []errorhandler.ErrorLink
	if strategy.FallbackUrl != "" {
		suggestion = fmt.Sprintf(
			"Install %s manually from: %s",
			tool.Name, strategy.FallbackUrl,
		)
		links = append(links, errorhandler.ErrorLink{
			URL:   strategy.FallbackUrl,
			Title: tool.Name + " installation instructions",
		})
	}

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"package manager %q not available on this platform",
			strategy.PackageManager,
		),
		Message:    "Cannot install " + tool.Name,
		Suggestion: suggestion,
		Links:      links,
	}
}

// managerUnavailableUninstallError builds an
// [errorhandler.ErrorWithSuggestion] for the case where the package
// manager required to remove a tool is not available on this platform.
func (i *installer) managerUnavailableUninstallError(
	tool *ToolDefinition,
	strategy *InstallStrategy,
) error {
	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"package manager %q not available on this platform",
			strategy.PackageManager,
		),
		Message: "Cannot uninstall " + tool.Name,
		Suggestion: fmt.Sprintf(
			"Package manager %q is not available. "+
				"Please remove %s manually using the tool you originally installed it with.",
			strategy.PackageManager, tool.Name,
		),
	}
}

// packageManagerUninstallFailedError builds an
// [errorhandler.ErrorWithSuggestion] for the specific case where the
// package manager is present but no longer has a record of a tool that azd
// still detects as installed. This is the signature of a self-updating CLI
// (for example one updated via its own `update` command) that replaced the
// copy the package manager installed. It is only used when
// packageManagerLostRecord matches; other failures surface the package
// manager's error directly. The package manager's reported message (when
// available) is echoed back, and the user is guided to delete the tool
// manually since neither the package manager nor azd can.
func (i *installer) packageManagerUninstallFailedError(
	tool *ToolDefinition,
	strategy *InstallStrategy,
	reported string,
) error {
	suggestion := fmt.Sprintf(
		"%s could not be removed with %s, which no longer has a record "+
			"of it. This usually means the tool was updated outside the "+
			"package manager (for example via its own update command). "+
			"Please remove %s manually.", tool.Name,
		strategy.PackageManager,
		tool.Name,
	)

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"running uninstall command for %s: %s", tool.Name, reported,
		),
		Message:    "Cannot uninstall " + tool.Name,
		Suggestion: suggestion,
	}
}

// vscodeDependencyConflict reports whether a failed `code
// --uninstall-extension` run was rejected because other installed
// extensions depend on the target extension. VS Code blocks this by
// design (even with --force), printing a message such as:
//
//	Cannot uninstall 'Azure Resources' extension. 'Azure App Service'
//	extension depends on this.
//
// When matched, the trimmed VS Code message is returned so callers can
// echo the specific dependent extensions back to the user.
func vscodeDependencyConflict(packageManager, stderr string) (string, bool) {
	if packageManager != "code" {
		return "", false
	}

	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "depend on this") ||
		strings.Contains(lower, "depends on this") {
		return strings.TrimSpace(stderr), true
	}

	return "", false
}

// packageManagerLostRecord reports whether a failed uninstall indicates the
// package manager no longer has a record of the package. This is
// winget-specific: winget tracks installs via the Windows Add/Remove
// Programs registry, so a self-updating CLI (for example `copilot update`
// after `winget install GitHub.Copilot`) can replace itself and unregister,
// leaving winget with no record even though the binary is still on PATH.
// winget then fails with APPINSTALLER_CLI_ERROR_NO_APPLICATIONS_FOUND and
// prints "No installed package found matching input criteria.".
//
// Other managers are intentionally excluded: brew, npm and apt track
// installs via their own manifests, which self-updates do not disturb, and
// their "nothing to remove" uninstalls typically exit 0 (npm, for example,
// prints "up to date"), so they do not reach this path. Their genuine
// failures surface directly instead, carrying the manager's own message.
func packageManagerLostRecord(packageManager string, res exec.RunResult) bool {
	if packageManager != "winget" {
		return false
	}

	// Windows exit codes are unsigned 32-bit values, so compare the low 32
	// bits to match regardless of how the sign bit is represented.
	exitCode := uint32(res.ExitCode) //nolint:gosec // 32-bit Windows exit code
	return exitCode == wingetNoPackageFoundExitCode ||
		strings.Contains(
			strings.ToLower(res.Stdout+"\n"+res.Stderr),
			"no installed package found",
		)
}

// vscodeDependencyUninstallError builds an
// [errorhandler.ErrorWithSuggestion] for the case where VS Code refuses to
// remove an extension because other installed extensions depend on it.
// Unlike packageManagerUninstallFailedError, this guides the user to
// remove the dependent extensions first rather than implying the tool was
// updated outside the package manager.
func (i *installer) vscodeDependencyUninstallError(
	tool *ToolDefinition,
	detail string,
) error {
	suggestion := fmt.Sprintf(
		"%s cannot be removed because other installed VS Code extensions "+
			"depend on it. Uninstall the dependent extension(s) first, then "+
			"remove %s.",
		tool.Name, tool.Name,
	)

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"running uninstall command for %s: %s", tool.Name, detail,
		),
		Message:    "Cannot uninstall " + tool.Name,
		Suggestion: suggestion,
	}
}

func (i *installer) uninstallUnsupportedError(
	tool *ToolDefinition,
	strategy *InstallStrategy,
) error {
	var links []errorhandler.ErrorLink
	if strategy.FallbackUrl != "" {
		links = append(links, errorhandler.ErrorLink{
			URL:   strategy.FallbackUrl,
			Title: tool.Name + " installation instructions",
		})
	}

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"no automated uninstall available for %s", tool.Name,
		),
		Message: "Cannot uninstall " + tool.Name,
		Suggestion: fmt.Sprintf(
			"azd cannot uninstall %s automatically. "+
				"Please remove it manually using the tool you originally installed it with.",
			tool.Name,
		),
		Links: links,
	}
}

// -----------------------------------------------------------------------
// Direct download + checksum verification
// -----------------------------------------------------------------------

// executeDirectDownload fetches the artifact from the strategy's
// DirectDownloadUrl, verifies the checksum (when configured), and
// places the downloaded file in a well-known location. The caller is
// responsible for post-download verification via the Detector.
func (i *installer) executeDirectDownload(
	ctx context.Context,
	strategy *InstallStrategy,
) error {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, strategy.DirectDownloadUrl, nil,
	)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading artifact: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"download failed with HTTP %d", resp.StatusCode,
		)
	}

	// Write to a temp file first.
	tmpFile, err := os.CreateTemp("", "azd-tool-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("writing download: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Verify checksum when configured.
	if err := validateChecksum(
		tmpPath, strategy.Checksum,
	); err != nil {
		return err
	}

	// Move the artifact to a permanent location.
	destDir, err := toolInstallDir()
	if err != nil {
		return fmt.Errorf("install dir: %w", err)
	}

	u, err := url.Parse(strategy.DirectDownloadUrl)
	if err != nil {
		return fmt.Errorf("parsing download URL: %w", err)
	}
	fileName := filepath.Base(u.Path)
	if fileName == "." || fileName == "/" || fileName == "" {
		return fmt.Errorf("cannot determine filename from download URL: %s", strategy.DirectDownloadUrl)
	}
	destPath := filepath.Join(destDir, fileName)

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating install dir: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		// Rename may fail across filesystems; fall back to
		// copy + remove.
		if cpErr := copyFilePath(tmpPath, destPath); cpErr != nil {
			return fmt.Errorf("placing artifact: %w", cpErr)
		}
	}

	// Make executable on Unix systems.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(destPath, 0o755); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
	}

	return nil
}

// executeDirectDownloadUninstall removes the artifact that
// executeDirectDownload placed in the well-known tool install
// directory. A missing file is treated as success so that uninstall is
// idempotent.
func (i *installer) executeDirectDownloadUninstall(
	strategy *InstallStrategy,
) error {
	destDir, err := toolInstallDir()
	if err != nil {
		return fmt.Errorf("install dir: %w", err)
	}

	u, err := url.Parse(strategy.DirectDownloadUrl)
	if err != nil {
		return fmt.Errorf("parsing download URL: %w", err)
	}
	fileName := filepath.Base(u.Path)
	if fileName == "." || fileName == "/" || fileName == "" {
		return fmt.Errorf(
			"cannot determine filename from download URL: %s",
			strategy.DirectDownloadUrl,
		)
	}
	destPath := filepath.Join(destDir, fileName)

	if err := os.Remove(destPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing artifact: %w", err)
	}

	return nil
}

// validateChecksum verifies the file at filePath against the
// expected checksum. When the checksum is empty (both Algorithm and
// Value are ""), validation is silently skipped. If only one of
// Algorithm or Value is set the configuration is treated as an error
// to prevent silent misconfiguration.
func validateChecksum(filePath string, checksum Checksum) error {
	if checksum.Algorithm == "" && checksum.Value == "" {
		return nil
	}

	// Reject partial checksum configuration.
	if checksum.Algorithm == "" {
		return fmt.Errorf(
			"checksum value is set but algorithm is empty" +
				" — specify both algorithm and value, or neither",
		)
	}
	if checksum.Value == "" {
		return fmt.Errorf(
			"checksum algorithm %q is set but value is empty"+
				" — specify both algorithm and value, or neither",
			checksum.Algorithm,
		)
	}

	var hashAlgo hash.Hash

	switch checksum.Algorithm {
	case "sha256":
		hashAlgo = sha256.New()
	case "sha512":
		hashAlgo = sha512.New()
	default:
		return fmt.Errorf(
			"unsupported checksum algorithm: %s",
			checksum.Algorithm,
		)
	}

	//nolint:gosec // filePath from controlled download
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf(
			"opening file for checksum: %w", err,
		)
	}
	defer file.Close()

	if _, err := io.Copy(hashAlgo, file); err != nil {
		return fmt.Errorf("computing checksum: %w", err)
	}

	actual := hex.EncodeToString(hashAlgo.Sum(nil))

	if !strings.EqualFold(actual, checksum.Value) {
		return fmt.Errorf(
			"checksum verification failed. "+
				"Expected: %s, Got: %s. "+
				"This may indicate a corrupted download",
			checksum.Value, actual,
		)
	}

	return nil
}

// toolInstallDir returns the directory where directly downloaded
// tools are placed. It respects the AZD_CONFIG_DIR environment
// variable via [config.GetUserConfigDir], falling back to
// ~/.azd/tools/ when the variable is unset.
func toolInstallDir() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("getting user config dir: %w", err)
	}
	return filepath.Join(configDir, "tools"), nil
}

// copyFilePath copies a file from src to dst using a byte stream.
func copyFilePath(src, dst string) error {
	//nolint:gosec // src is a controlled temp file
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// -----------------------------------------------------------------------
// Package-manager command builders
// -----------------------------------------------------------------------

// buildManagerCommand returns the command and arguments for a
// well-known package manager install or upgrade operation. cask applies
// only to Homebrew, adding the `--cask` flag.
func buildManagerCommand(
	manager string,
	packageID string,
	upgrade bool,
	cask bool,
) (string, []string) {
	switch manager {
	case "winget":
		return buildWingetCommand(packageID, upgrade)
	case "brew":
		return buildBrewCommand(packageID, upgrade, cask)
	case "apt":
		return buildAptCommand(packageID, upgrade)
	case "npm":
		return buildNpmCommand(packageID, upgrade)
	case "code":
		return buildCodeCommand(packageID, upgrade)
	default:
		return "", nil
	}
}

func buildWingetCommand(
	packageID string, upgrade bool,
) (string, []string) {
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	return "winget", []string{
		action,
		"--id", packageID,
		"--accept-source-agreements",
		"--accept-package-agreements",
		"-e",
	}
}

func buildBrewCommand(
	packageID string, upgrade bool, cask bool,
) (string, []string) {
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	args := []string{action}
	if cask {
		args = append(args, "--cask")
	}
	args = append(args, packageID)
	return "brew", args
}

func buildAptCommand(
	packageID string, upgrade bool,
) (string, []string) {
	if upgrade {
		return "sudo", []string{
			"apt-get", "install",
			"--only-upgrade", "-y", packageID,
		}
	}
	return "sudo", []string{
		"apt-get", "install", "-y", packageID,
	}
}

func buildNpmCommand(
	packageID string, upgrade bool,
) (string, []string) {
	if upgrade {
		return "npm", []string{"update", "-g", packageID}
	}
	return "npm", []string{"install", "-g", packageID}
}

func buildCodeCommand(
	packageID string, upgrade bool,
) (string, []string) {
	args := []string{"--install-extension", packageID}
	if upgrade {
		args = append(args, "--force")
	}
	return "code", args
}

// buildUninstallCommand returns the command and arguments to remove a
// package previously installed by the given package manager. cask applies
// only to Homebrew, adding the `--cask` flag. It returns an empty command
// for unknown managers.
func buildUninstallCommand(
	manager string,
	packageID string,
	cask bool,
) (string, []string) {
	switch manager {
	case "winget":
		return "winget", []string{
			"uninstall",
			"--id", packageID,
			"--accept-source-agreements",
			"-e",
		}
	case "brew":
		args := []string{"uninstall"}
		if cask {
			args = append(args, "--cask")
		}
		args = append(args, packageID)
		return "brew", args
	case "apt":
		return "sudo", []string{"apt-get", "remove", "-y", packageID}
	case "npm":
		return "npm", []string{"uninstall", "-g", packageID}
	case "code":
		return "code", []string{"--uninstall-extension", packageID}
	default:
		return "", nil
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// splitCommand splits a whitespace-delimited command string into the executable
// name and its arguments. Note: this uses strings.Fields which does not handle
// quoted arguments (e.g., --path "Program Files"). Commands requiring shell
// features like quoting, pipes, or redirections are routed through the shell
// via containsShellOperators, bypassing this function.
func splitCommand(command string) (string, []string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// containsShellOperators reports whether the command string contains
// shell metacharacters (pipes, redirects, background operators, or
// command chaining) that require execution through a system shell.
func containsShellOperators(cmd string) bool {
	return strings.ContainsAny(cmd, "|><;") || strings.Contains(cmd, "&&")
}

// executeShellCommand runs a command string through the system shell
// so that shell operators such as pipes and redirects are
// interpreted correctly.
func (i *installer) executeShellCommand(
	ctx context.Context,
	command string,
) error {
	var shell string
	var args []string

	if runtime.GOOS == "windows" {
		shell = "cmd"
		args = []string{"/C", command}
	} else {
		shell = "sh"
		args = []string{"-c", command}
	}

	runArgs := exec.NewRunArgs(shell, args...)
	_, err := i.commandRunner.Run(ctx, runArgs)
	return err
}
