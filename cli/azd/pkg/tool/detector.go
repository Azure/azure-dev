// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"regexp"
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
	// Error records any unexpected failure during detection (e.g. a
	// timeout). A tool that is simply not installed has Error == nil.
	Error error
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
	case ToolCategoryExtension:
		return d.detectExtension(ctx, tool), nil
	case ToolCategoryServer, ToolCategoryLibrary:
		return d.detectCommandBased(ctx, tool), nil
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

		// Non-zero exit: the binary exists, so try to parse
		// the version from whatever output was captured.
	}

	status.Installed = true
	status.InstalledVersion = matchVersion(
		result.Stdout+result.Stderr, tool.VersionRegex,
	)

	return status
}

// detectExtension checks for a VS Code extension by listing
// installed extensions with `code --list-extensions --show-versions`
// and matching the output against the tool's [ToolDefinition.VersionRegex].
func (d *detector) detectExtension(
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

// detectCommandBased handles server and library tools that specify a
// DetectCommand. If no DetectCommand is configured the tool is
// reported as not installed.
func (d *detector) detectCommandBased(
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

		// Non-zero exit: the binary exists, so try to parse
		// any captured output.
	}

	status.Installed = true
	status.InstalledVersion = matchVersion(
		result.Stdout+result.Stderr, tool.VersionRegex,
	)

	return status
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// matchVersion compiles the regex pattern and returns the first
// capture-group match from output. Returns "" when the pattern is
// empty, the regex is invalid, or no match is found.
func matchVersion(output, pattern string) string {
	if pattern == "" || output == "" {
		return ""
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
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
