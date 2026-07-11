// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	osexec "os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// ToolStatus captures the detection result for a single tool.
type ToolStatus struct {
	// Tool is the definition that was probed.
	Tool *ToolDefinition
	// Installed is true when the tool was found on the local machine.
	Installed bool
	// InstalledVersion is the version string extracted from the tool's
	// output. It is empty when the tool is not installed or when
	// version parsing fails.
	InstalledVersion string
	// SkillHosts lists every agentic CLI host the skill is installed
	// through, with the version installed via each, in manifest order.
	// Populated only for skill tools (see detectSkill); nil otherwise.
	SkillHosts []InstalledSkillHost
	// Error records any unexpected failure during detection (e.g. a
	// timeout). A tool that is simply not installed has Error == nil.
	Error error
}

// InstalledSkillHost pairs an agentic CLI host with the skill version
// installed through it.
type InstalledSkillHost struct {
	// Host is the agentic CLI host binary name (e.g. "copilot").
	Host string
	// Version is the skill version installed through that host.
	Version string
}

// Detector checks whether tools are installed and extracts their
// versions.
type Detector interface {
	// DetectTool probes a single tool and returns its status.
	DetectTool(
		ctx context.Context,
		tool *ToolDefinition,
	) (*ToolStatus, error)

	// DetectAll probes every tool concurrently and returns a status
	// entry for each one. Individual detection failures are captured
	// in [ToolStatus.Error]; the returned error is non-nil only for
	// programming mistakes such as a nil tool pointer.
	DetectAll(
		ctx context.Context,
		tools []*ToolDefinition,
	) ([]*ToolStatus, error)

	// DetectSkillHosts returns every configured SkillHost the skill is
	// currently installed through (with the version installed via each),
	// in manifest order. It returns nil for non-skill tools. The returned
	// error is non-nil only for context cancellation/timeout while
	// listing plugins.
	DetectSkillHosts(
		ctx context.Context,
		tool *ToolDefinition,
	) ([]InstalledSkillHost, error)
}

type detector struct {
	commandRunner exec.CommandRunner
}

// NewDetector creates a [Detector] backed by the given
// [exec.CommandRunner].
func NewDetector(commandRunner exec.CommandRunner) Detector {
	return &detector{commandRunner: commandRunner}
}

// DetectTool dispatches to category-specific detection logic.
func (d *detector) DetectTool(
	ctx context.Context,
	tool *ToolDefinition,
) (*ToolStatus, error) {
	if tool == nil {
		return nil, errors.New(
			"tool definition must not be nil",
		)
	}

	switch tool.Category {
	case ToolCategoryCLI:
		return d.detectCLI(ctx, tool), nil
	case ToolCategoryVSCodeExtension:
		return d.detectVSCodeExtension(ctx, tool), nil
	case ToolCategoryServer:
		return d.detectServer(ctx, tool), nil
	case ToolCategoryAzdExtension:
		return d.detectAzdExtension(ctx, tool), nil
	case ToolCategorySkill:
		return d.detectSkill(ctx, tool), nil
	default:
		return &ToolStatus{Tool: tool}, nil
	}
}

// DetectAll probes every tool concurrently using
// [sync.WaitGroup.Go] and collects the results. It never fails
// fast — every tool gets a chance to complete regardless of
// individual errors.
func (d *detector) DetectAll(
	ctx context.Context,
	tools []*ToolDefinition,
) ([]*ToolStatus, error) {
	results := make([]*ToolStatus, len(tools))

	var wg sync.WaitGroup

	for i, t := range tools {
		if t == nil {
			results[i] = &ToolStatus{}
			continue
		}

		wg.Go(func() {
			// DetectTool only returns an error for nil tools,
			// which we guard above.
			status, _ := d.DetectTool(ctx, t)
			results[i] = status
		})
	}

	wg.Wait()

	return results, nil
}

// ---------------------------------------------------------------------------
// Category-specific detectors
// ---------------------------------------------------------------------------

// detectCLI checks for a standalone CLI binary on PATH and extracts
// its version by running the configured version command.
func (d *detector) detectCLI(
	ctx context.Context,
	tool *ToolDefinition,
) *ToolStatus {
	status := &ToolStatus{Tool: tool}

	if tool.DetectCommand == "" {
		return status
	}

	// Fast-path: if the binary is not on PATH it is not installed.
	if err := d.commandRunner.ToolInPath(
		tool.DetectCommand,
	); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return status
		}

		status.Error = fmt.Errorf(
			"checking PATH for %s: %w",
			tool.DetectCommand, err,
		)

		return status
	}

	// Run the version command. Even on a non-zero exit the
	// RunResult contains captured stdout/stderr that may hold a
	// version string.
	result, err := d.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  tool.DetectCommand,
		Args: tool.VersionArgs,
	})

	if err != nil {
		if isNotFoundErr(err) {
			return status
		}

		if isContextErr(err) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		// Only treat as installed if the error is an exit error
		// (non-zero exit code). Other errors (permission denied,
		// missing shared libraries, etc.) are recorded and returned
		// without marking installed.
		var exitErr *osexec.ExitError
		if !errors.As(err, &exitErr) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		// Non-zero exit: the binary exists, so try to parse
		// the version from whatever output was captured.
	}

	status.Installed = true
	status.InstalledVersion = matchVersion(
		result.Stdout+result.Stderr, tool.VersionRegex,
	)

	return status
}

// detectVSCodeExtension checks for a VS Code extension by listing
// installed extensions with `code --list-extensions --show-versions`
// and matching the output against the tool's [ToolDefinition.VersionRegex].
func (d *detector) detectVSCodeExtension(
	ctx context.Context,
	tool *ToolDefinition,
) *ToolStatus {
	status := &ToolStatus{Tool: tool}

	detectCmd := tool.DetectCommand
	if detectCmd == "" {
		detectCmd = "code"
	}

	// VS Code must be on PATH for extension detection.
	if err := d.commandRunner.ToolInPath(
		detectCmd,
	); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return status
		}

		status.Error = fmt.Errorf(
			"checking PATH for %s: %w", detectCmd, err,
		)

		return status
	}

	result, err := d.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  detectCmd,
		Args: tool.VersionArgs,
	})

	if err != nil {
		if isNotFoundErr(err) {
			return status
		}

		if isContextErr(err) {
			status.Error = fmt.Errorf(
				"listing VS Code extensions: %w", err,
			)
			return status
		}

		// Only treat as potentially installed if the error is an
		// exit error. Other errors are recorded without marking installed.
		var exitErr *osexec.ExitError
		if !errors.As(err, &exitErr) {
			status.Error = fmt.Errorf(
				"listing VS Code extensions: %w", err,
			)
			return status
		}

		// Non-zero exit but output may still be usable.
	}

	version := matchVersion(
		result.Stdout+result.Stderr, tool.VersionRegex,
	)

	if version != "" {
		status.Installed = true
		status.InstalledVersion = version
	}

	return status
}

// detectServer handles server tools that specify a DetectCommand.
// If no DetectCommand is configured the tool is reported as not installed.
func (d *detector) detectServer(
	ctx context.Context,
	tool *ToolDefinition,
) *ToolStatus {
	status := &ToolStatus{Tool: tool}

	if tool.DetectCommand == "" {
		return status
	}

	if err := d.commandRunner.ToolInPath(
		tool.DetectCommand,
	); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return status
		}

		status.Error = fmt.Errorf(
			"checking PATH for %s: %w",
			tool.DetectCommand, err,
		)

		return status
	}

	if len(tool.VersionArgs) == 0 {
		// The command exists but there is nothing to run for
		// version extraction.
		status.Installed = true
		return status
	}

	result, err := d.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  tool.DetectCommand,
		Args: tool.VersionArgs,
	})

	if err != nil {
		if isNotFoundErr(err) {
			return status
		}

		if isContextErr(err) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		// Only treat as potentially installed if the error is an
		// exit error. Other errors are recorded without marking installed.
		var exitErr *osexec.ExitError
		if !errors.As(err, &exitErr) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		// Non-zero exit: the binary exists, so try to parse
		// any captured output.
	}

	version := matchVersion(
		result.Stdout+result.Stderr, tool.VersionRegex,
	)

	if version != "" {
		status.Installed = true
		status.InstalledVersion = version
	} else if tool.VersionRegex == "" {
		// No regex configured — the command ran successfully,
		// so treat the binary as installed.
		status.Installed = true
	}

	return status
}

// ---------------------------------------------------------------------------
// JSON-based library detection
// ---------------------------------------------------------------------------

// azdExtensionEntry represents a single entry in the JSON output of
// `azd extension list --installed --output json`.
type azdExtensionEntry struct {
	ID               string `json:"id"`
	InstalledVersion string `json:"installedVersion"`
}

// detectAzdExtension handles azd extension tools by parsing JSON
// output from `azd extension list --installed --output json`. It
// looks for an entry whose `id` matches the tool's [ToolDefinition.Id]
// and extracts the `installedVersion`.
func (d *detector) detectAzdExtension(
	ctx context.Context,
	tool *ToolDefinition,
) *ToolStatus {
	status := &ToolStatus{Tool: tool}

	if tool.DetectCommand == "" {
		return status
	}

	if err := d.commandRunner.ToolInPath(
		tool.DetectCommand,
	); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return status
		}

		status.Error = fmt.Errorf(
			"checking PATH for %s: %w",
			tool.DetectCommand, err,
		)

		return status
	}

	if len(tool.VersionArgs) == 0 {
		status.Installed = true
		return status
	}

	result, err := d.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  tool.DetectCommand,
		Args: tool.VersionArgs,
	})

	if err != nil {
		if isNotFoundErr(err) {
			return status
		}

		if isContextErr(err) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		var exitErr *osexec.ExitError
		if !errors.As(err, &exitErr) {
			status.Error = fmt.Errorf(
				"running %s: %w", tool.DetectCommand, err,
			)
			return status
		}

		// Non-zero exit: try to parse any captured output.
	}

	var extensions []azdExtensionEntry
	if jsonErr := json.Unmarshal(
		[]byte(result.Stdout), &extensions,
	); jsonErr != nil {
		// JSON parsing failed — fall back to not installed.
		return status
	}

	for _, ext := range extensions {
		if ext.ID == tool.Id {
			status.Installed = true
			status.InstalledVersion = ext.InstalledVersion
			return status
		}
	}

	return status
}

// ---------------------------------------------------------------------------
// Skill detection
// ---------------------------------------------------------------------------

// detectSkill checks whether a skill is installed by probing every
// SkillHost's PluginListCommand. A host is reported as having the skill
// installed only when its PluginName appears in the listing AND its
// (required) VersionRegex captures a version. Because a host's list
// command reports every installed plugin, the regex anchors on this
// skill's identity so another plugin's version is never mistaken for
// it. Every host the skill is installed through is recorded in
// SkillHosts (in manifest order); Installed and InstalledVersion reflect
// the first such host, so a skill installed anywhere reads as installed.
// Hosts whose binary is not on PATH are skipped silently — a missing
// host is not an error, it just means the skill cannot be installed
// through that host.
func (d *detector) detectSkill(
	ctx context.Context,
	tool *ToolDefinition,
) *ToolStatus {
	status := &ToolStatus{Tool: tool}

	if len(tool.SkillHosts) == 0 {
		return status
	}

	hosts, err := d.DetectSkillHosts(ctx, tool)
	status.SkillHosts = hosts
	if len(hosts) > 0 {
		// The skill was found on at least one host; report it installed even if
		// a later host's probe errored (e.g. was cancelled), mirroring the
		// first-match behavior from before multi-host detection. Installed and
		// InstalledVersion reflect the first such host.
		status.Installed = true
		status.InstalledVersion = hosts[0].Version
		return status
	}

	// Nothing was found: surface a detection error (e.g. context cancellation)
	// so a genuinely failed probe is not silently reported as "not installed".
	if err != nil {
		status.Error = err
	}

	return status
}

// DetectSkillHosts returns every configured SkillHost the skill is
// currently installed through (with the version installed via each), in
// manifest order. Unlike detectSkill (which stops at the first match) it
// probes all hosts, so callers can act on every install — e.g.
// `azd tool upgrade` refreshing the skill on each host it was installed
// to, or per-host install verification.
func (d *detector) DetectSkillHosts(
	ctx context.Context,
	tool *ToolDefinition,
) ([]InstalledSkillHost, error) {
	if tool == nil {
		return nil, errors.New("tool definition must not be nil")
	}
	if tool.Category != ToolCategorySkill {
		return nil, nil
	}

	var hosts []InstalledSkillHost
	for _, host := range tool.SkillHosts {
		version, err := d.skillHostVersion(ctx, host)
		if err != nil {
			// Return the hosts discovered before the error (e.g. a later host's
			// probe was cancelled) so a caller — detectSkill in particular — can
			// still act on an earlier match instead of losing it.
			return hosts, err
		}
		if version != "" {
			hosts = append(hosts, InstalledSkillHost{
				Host:    host.Command,
				Version: version,
			})
		}
	}
	return hosts, nil
}

// skillHostVersion reports the version of the skill as installed through
// a single host, or "" when the host is not on PATH, its list command
// fails, or the skill is not present. The error is non-nil only for
// context cancellation/timeout.
//
// It uses the same two-stage gate as the rest of skill detection to
// avoid false positives: PluginName must appear in stdout AND
// VersionRegex must capture a version. The regex anchors on this
// skill's identity (the azure@azure-skills entry in claude's `plugin
// list --json` output, or the plugin name in copilot's `plugin list`),
// so a host that lists other plugins but not this skill is reported as
// not installed.
func (d *detector) skillHostVersion(
	ctx context.Context,
	host SkillHost,
) (string, error) {
	if len(host.PluginListCommand) == 0 || host.PluginName == "" {
		return "", nil
	}

	// A host that is not on PATH cannot have the skill installed through
	// it; skip silently. Probe the exec binary (Command), not the display
	// Host, so this PATH check matches the command actually run below —
	// otherwise a manifest whose Host differs from its binary (e.g. Host
	// "Claude Code CLI" / Command "claude") is never detected and a
	// just-completed install fails verification.
	if err := d.commandRunner.ToolInPath(host.Command); err != nil {
		return "", nil
	}

	// Run the list command. If it fails for any reason other than a
	// context error we cannot reliably tell whether the skill is
	// installed via this host — fail closed and report not-installed
	// rather than guessing from the error output (which often echoes the
	// queried name).
	result, err := d.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  host.Command,
		Args: host.PluginListCommand,
	})
	if err != nil {
		if isContextErr(err) {
			return "", fmt.Errorf("listing %s plugins: %w", host.Host, err)
		}
		return "", nil
	}

	// Match only against stdout: stderr is usually diagnostics, not the
	// canonical listing.
	if !strings.Contains(result.Stdout, host.PluginName) {
		return "", nil
	}
	return matchVersion(result.Stdout, host.VersionRegex), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// regexCache stores compiled regular expressions to avoid recompilation
// on every detection call.
var regexCache sync.Map

// matchVersion looks up or compiles the regex pattern and returns the
// first capture-group match from output. Returns "" when the pattern is
// empty, the regex is invalid, or no match is found.
func matchVersion(output, pattern string) string {
	if pattern == "" || output == "" {
		return ""
	}

	var re *regexp.Regexp
	if cached, ok := regexCache.Load(pattern); ok {
		re = cached.(*regexp.Regexp)
	} else {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return ""
		}
		regexCache.Store(pattern, compiled)
		re = compiled
	}

	m := re.FindStringSubmatch(output)
	if len(m) < 2 {
		return ""
	}

	return m[1]
}

// isNotFoundErr reports whether err indicates the command binary
// could not be located.
func isNotFoundErr(err error) bool {
	return errors.Is(err, osexec.ErrNotFound)
}

// isContextErr reports whether err originated from a cancelled or
// timed-out context.
func isContextErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}
