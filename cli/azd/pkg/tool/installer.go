// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// InstallResult captures the outcome of an install or upgrade operation.
type InstallResult struct {
	// Tool is the definition that was installed or upgraded.
	Tool *ToolDefinition
	// Success indicates whether the operation completed successfully
	// and the tool is now available on the local machine.
	Success bool
	// InstalledVersion is the version detected after installation.
	InstalledVersion string
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
	// optional [WithHosts] option restricts installation to the named
	// agentic CLI hosts.
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

	// AvailableSkillHosts returns the names of the tool's configured
	// SkillHosts that are currently on PATH, in manifest order
	// (preferred host first). It returns nil for non-skill tools or
	// when none of the hosts are available.
	AvailableSkillHosts(tool *ToolDefinition) []string
}

// installConfig holds the resolved options for an install or upgrade
// operation.
type installConfig struct {
	// hosts, when non-empty, restricts a skill install/upgrade to the
	// named agentic CLI hosts (e.g. "copilot", "claude"). An empty slice
	// selects the single preferred host (the first configured host on
	// PATH). Ignored for non-skill tools.
	hosts []string
}

// InstallOption customizes an install or upgrade operation.
type InstallOption func(*installConfig)

// WithHosts restricts a skill install/upgrade to the named agentic CLI
// hosts. It is ignored for non-skill tools. Passing no hosts (or not
// using this option) selects the single preferred host.
func WithHosts(hosts ...string) InstallOption {
	return func(c *installConfig) { c.hosts = hosts }
}

// installer is the default, unexported implementation of [Installer].
type installer struct {
	commandRunner    exec.CommandRunner
	platformDetector *PlatformDetector
	detector         Detector
	httpClient       httpDoer
	platformMu       sync.Mutex
	platform         *Platform // lazily populated by ensurePlatform
	// retryBackoff is the initial wait between post-install detection
	// retries (doubled each attempt). Defaults to 1s; tests may shorten
	// it to keep the suite fast.
	retryBackoff time.Duration
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

// AvailableSkillHosts returns the names of the tool's configured
// SkillHosts that are currently on PATH, in manifest order (preferred
// host first). It returns nil for non-skill tools or when none of the
// hosts are available.
func (i *installer) AvailableSkillHosts(tool *ToolDefinition) []string {
	if tool.Category != ToolCategorySkill {
		return nil
	}
	var present []string
	for _, host := range tool.SkillHosts {
		if i.commandRunner.ToolInPath(host.Host) == nil {
			present = append(present, host.Host)
		}
	}
	return present
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

	// Skills follow a different flow: they install through the host
	// agentic CLI native plugin command rather than the platform's
	// package manager, so we short-circuit before platform detection.
	if tool.Category == ToolCategorySkill {
		return i.runSkill(ctx, tool, upgrade, cfg.hosts)
	}

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
	backoff := i.retryBackoff

	for attempt := range maxAttempts {
		status, err = i.detector.DetectTool(ctx, tool)
		if err != nil {
			result.Error = fmt.Errorf(
				"verifying installation of %s: %w",
				tool.Name, err,
			)
			result.Duration = time.Since(start)
			return result, nil
		}

		if status.Installed {
			break
		}

		// No more retries left — fall through to the failure path.
		if attempt >= maxAttempts-1 {
			break
		}

		log.Printf(
			"installer: %s not yet detected, retrying in %s (attempt %d/%d)",
			tool.Name, backoff, attempt+1, maxAttempts-1,
		)

		select {
		case <-ctx.Done():
			result.Error = fmt.Errorf(
				"verifying installation of %s: %w",
				tool.Name, ctx.Err(),
			)
			result.Duration = time.Since(start)
			return result, nil
		case <-time.After(backoff):
		}

		backoff *= 2
	}

	if !status.Installed {
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

// ---------------------------------------------------------------------------
// Skill install / upgrade
// ---------------------------------------------------------------------------

// runSkill installs (or upgrades) a skill across one or more agentic CLI
// hosts.
//
// Prerequisite rules:
//  1. HARD — at least one supported agentic CLI host (copilot or claude)
//     must be on PATH. We do NOT install one ourselves; if none is
//     present resolveSkillTargets fails with an
//     [errorhandler.ErrorWithSuggestion] pointing at the install docs.
//  2. SOFT — Node.js (`node`) on PATH. The Azure MCP server is started
//     via `npx`, so its absence breaks MCP-backed scenarios but does NOT
//     prevent installing the skill files. We warn and continue.
//  3. Git is NOT pre-checked. The host CLI fetches the skill repo itself
//     and surfaces its own diagnostic when git is missing.
//
// The hosts argument, when non-empty, restricts the operation to the
// named hosts; otherwise the single preferred host (first on PATH) is
// used.
func (i *installer) runSkill(
	ctx context.Context,
	tool *ToolDefinition,
	upgrade bool,
	hosts []string,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	if len(tool.SkillHosts) == 0 {
		result.Error = fmt.Errorf("%s has no SkillHosts configured", tool.Name)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 1. HARD prerequisite: resolve the target host(s).
	targets, err := i.resolveSkillTargets(ctx, tool, hosts, upgrade)
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

	// 3. Install / upgrade for each target host, collecting outcomes.
	var (
		succeeded []string
		failures  []error
		version   string
	)
	for _, host := range targets {
		hostVersion, hostErr := i.installSkillForHost(ctx, tool, host, upgrade)
		if hostErr != nil {
			failures = append(failures, fmt.Errorf("%s: %w", host.Host, hostErr))
			continue
		}
		succeeded = append(succeeded, host.Host)
		if version == "" {
			version = hostVersion
		}
	}

	result.Strategy = strings.Join(succeeded, ", ")
	result.InstalledVersion = version
	result.Duration = time.Since(start)

	// On failure, preserve the wrapped error for a single host so callers
	// can match it with errors.Is/As; summarize when several hosts fail.
	if len(failures) > 0 {
		if len(failures) == 1 {
			result.Error = failures[0]
		} else {
			msgs := make([]string, len(failures))
			for j, f := range failures {
				msgs[j] = f.Error()
			}
			result.Error = fmt.Errorf(
				"%s could not be installed for %d host(s): %s",
				tool.Name, len(failures), strings.Join(msgs, "; "),
			)
		}
		return result, nil
	}

	result.Success = true
	return result, nil
}

// resolveSkillTargets resolves the host(s) a skill should be installed
// to. With no explicit selection it returns a single host: for an
// upgrade, the host the detector reports the skill is installed through
// (falling back to the preferred host); otherwise the preferred host
// (via pickSkillHost). With an explicit selection every named host must
// be a configured SkillHost that is on PATH; otherwise an error naming
// the available hosts is returned.
func (i *installer) resolveSkillTargets(
	ctx context.Context,
	tool *ToolDefinition,
	hosts []string,
	upgrade bool,
) ([]SkillHost, error) {
	if len(hosts) == 0 {
		// For an upgrade, prefer the host that already has the skill so we
		// don't run an update against a host that never installed it (e.g.
		// updating copilot when the skill was installed via claude).
		if upgrade {
			if status, err := i.detector.DetectTool(ctx, tool); err == nil &&
				status.Installed && status.InstalledHost != "" {
				idx := slices.IndexFunc(tool.SkillHosts, func(h SkillHost) bool {
					return h.Host == status.InstalledHost
				})
				if idx >= 0 {
					return []SkillHost{tool.SkillHosts[idx]}, nil
				}
			}
		}
		host, err := i.pickSkillHost(tool)
		if err != nil {
			return nil, err
		}
		return []SkillHost{host}, nil
	}

	targets := make([]SkillHost, 0, len(hosts))
	for _, name := range hosts {
		// A requested host is usable only if it is a configured SkillHost
		// that is also on PATH. "unknown name" and "known but not on PATH"
		// both mean the host can't be used, so we point the user at the
		// supported hosts.
		idx := slices.IndexFunc(tool.SkillHosts, func(h SkillHost) bool {
			return h.Host == name
		})
		if idx < 0 || i.commandRunner.ToolInPath(name) != nil {
			supported := make([]string, len(tool.SkillHosts))
			for j, h := range tool.SkillHosts {
				supported[j] = h.Host
			}
			return nil, fmt.Errorf(
				"host %s not found on PATH for %s",
				strings.Join(supported, " or "), tool.Name,
			)
		}
		targets = append(targets, tool.SkillHosts[idx])
	}
	return targets, nil
}

// pickSkillHost returns the first SkillHost whose binary is on PATH.
// When none of the configured hosts is available it returns an
// [errorhandler.ErrorWithSuggestion] (all four fields populated per the
// AGENTS.md completeness rule) that recommends installing GitHub
// Copilot CLI via `azd tool install github-copilot-cli` — a single
// command the user can copy-paste without leaving azd.
func (i *installer) pickSkillHost(
	tool *ToolDefinition,
) (SkillHost, error) {
	var checked []string
	for _, host := range tool.SkillHosts {
		if err := i.commandRunner.ToolInPath(host.Host); err == nil {
			return host, nil
		}
		checked = append(checked, host.Host)
	}

	suggestion := fmt.Sprintf(
		"%s installs through your existing agentic CLI. Install GitHub "+
			"Copilot CLI:\n\n"+
			"    azd tool install github-copilot-cli\n\n"+
			"Then re-run `azd tool install %s`.\n"+
			"Checked (none found on PATH): %s",
		tool.Name, tool.Id, strings.Join(checked, ", "),
	)

	return SkillHost{}, &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"no supported agentic CLI host found on PATH for %s",
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

// installSkillForHost installs (or upgrades) the skill through a single
// host and verifies the result, returning the detected version.
func (i *installer) installSkillForHost(
	ctx context.Context,
	tool *ToolDefinition,
	host SkillHost,
	upgrade bool,
) (string, error) {
	if err := i.runSkillHostCommand(ctx, host, upgrade); err != nil {
		return "", err
	}

	status, err := i.verifySkillInstalled(ctx, tool, host)
	if err != nil {
		return "", err
	}
	return status.InstalledVersion, nil
}

// verifySkillInstalled confirms the skill is detectable after install.
// Plugin-list output sometimes lags the install action (the pre-existing
// copilot CLI integration documents the same race — see
// internal/agent/copilot/cli.go), so it retries a few times with
// exponential backoff to match the package-manager install path.
func (i *installer) verifySkillInstalled(
	ctx context.Context,
	tool *ToolDefinition,
	host SkillHost,
) (*ToolStatus, error) {
	const maxAttempts = 4 // 1 initial + 3 retries
	var status *ToolStatus
	backoff := i.retryBackoff

	for attempt := range maxAttempts {
		var err error
		status, err = i.detector.DetectTool(ctx, tool)
		if err != nil {
			return nil, fmt.Errorf(
				"verifying installation of %s: %w", tool.Name, err,
			)
		}

		if status.Installed {
			return status, nil
		}

		if attempt >= maxAttempts-1 {
			break
		}

		log.Printf(
			"installer: %s not yet detected, retrying in %s (attempt %d/%d)",
			tool.Name, backoff, attempt+1, maxAttempts-1,
		)

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf(
				"verifying installation of %s: %w",
				tool.Name, ctx.Err(),
			)
		case <-time.After(backoff):
		}

		backoff *= 2
	}

	return nil, fmt.Errorf(
		"%s was installed via %s but verification failed",
		tool.Name, host.Host,
	)
}

// runSkillHostCommand executes the host's install (or update) command
// with stdin/stdout/stderr connected to the user (WithInteractive=true)
// so any prompts the host CLI surfaces are answered by the user
// directly. azd never pipes canned answers on the user's behalf.
//
// For fresh installs it first runs MarketplaceAddCommand when the host
// declares one. Hosts that declare no MarketplaceAddCommand skip this
// step entirely.
//
// A non-zero exit is returned to the caller as an error; the caller is
// expected to verify via the detector and decide whether to treat the
// error as fatal (some hosts return non-zero on idempotent re-install).
func (i *installer) runSkillHostCommand(
	ctx context.Context,
	host SkillHost,
	upgrade bool,
) error {
	cmd := host.PluginInstallCommand
	verb := "install"
	if upgrade {
		cmd = host.PluginUpdateCommand
		verb = "update"
	}
	if len(cmd) == 0 {
		return fmt.Errorf(
			"host %q has no %s command configured", host.Host, verb,
		)
	}

	if !upgrade && len(host.MarketplaceAddCommand) > 0 {
		if err := i.runMarketplaceAdd(ctx, host); err != nil {
			return err
		}
	}

	runArgs := exec.NewRunArgs(host.Host, cmd...).WithInteractive(true)
	if _, err := i.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf(
			"running `%s %s`: %w",
			host.Host, strings.Join(cmd, " "), err,
		)
	}

	return nil
}

// runMarketplaceAdd registers the skill marketplace with the host CLI.
// Some hosts (e.g. copilot) return a non-zero exit when the marketplace
// is already registered; we recognize that case from the captured
// output and treat it as success so the install can proceed. Hosts that
// already exit 0 in the "already added" case (e.g. claude) flow
// through naturally. Any other failure is returned to the caller.
func (i *installer) runMarketplaceAdd(
	ctx context.Context,
	host SkillHost,
) error {
	args := exec.NewRunArgs(host.Host, host.MarketplaceAddCommand...)
	result, err := i.commandRunner.Run(ctx, args)
	if err == nil {
		return nil
	}
	if isMarketplaceAlreadyAdded(result.Stdout + result.Stderr) {
		return nil
	}
	return fmt.Errorf(
		"running `%s %s`: %w",
		host.Host, strings.Join(host.MarketplaceAddCommand, " "), err,
	)
}

// isMarketplaceAlreadyAdded reports whether the host CLI output indicates
// the marketplace is already registered. Observed wording per host:
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
// well-known package manager install or upgrade operation.
func buildManagerCommand(
	manager string,
	packageID string,
	upgrade bool,
) (string, []string) {
	switch manager {
	case "winget":
		return buildWingetCommand(packageID, upgrade)
	case "brew":
		return buildBrewCommand(packageID, upgrade)
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
	packageID string, upgrade bool,
) (string, []string) {
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	return "brew", []string{action, packageID}
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
