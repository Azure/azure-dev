// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// ext_lookup.go provides helpers for talking to the azd host's extension
// layer from inside this extension. Two responsibilities:
//
//  1. Detect whether a sibling extension (e.g. azure.ai.docs) is
//     installed locally so a cross-extension dispatch is safe.
//  2. Run a child `azd` subprocess to invoke another extension's
//     command (skill install, ext install, etc.).
//
// Both helpers shell out to `azd` because the gRPC SDK does not (yet)
// expose extension-management RPCs from inside an extension. Pattern
// matches the existing exec.Command("azd", ...) sites in
// microsoft.azd.extensions and microsoft.azd.concurx.
//
// # Why pre-check instead of relying on azd's built-in auto-install
//
// `azd` ships an auto-install feature (cli/azd/cmd/auto_install.go)
// that detects when a command belongs to an uninstalled extension and
// offers to install it. In `--no-prompt` mode `console.Confirm` returns
// the prompt's DefaultValue (`true` for the auto-install prompt), so in
// theory shelling out to `azd ai doc skills install --no-prompt` would
// silently install azure.ai.docs and re-run the command.
//
// In practice the re-run breaks for our use case. The pre-parser
// `extractFlagsWithValues` only knows about flags declared on the
// CURRENT command tree -- extension-specific flags like `--target` and
// `--path` do not exist until azure.ai.docs is installed. So the
// pre-parser treats `copilot` (a `--target` value) and `json` (an
// `--output` value) as positional args, mis-detects the command, and
// the re-run fails with `unknown flag: --target` even though the
// extension was just installed successfully.
//
// Pre-checking with `azd ext list -o json` + an explicit consent
// prompt + an explicit `azd ext install` shell-out avoids this entirely
// because we only dispatch the install command once azure.ai.docs is
// known to be present. As a bonus the parent process owns the consent
// UX (single clean prompt) instead of the child emitting a surprise
// warning mid-flow, and CI users get one clear "install azure.ai.docs"
// hint from us instead of the two scattered messages auto-install
// produces.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// extListItem mirrors the wire shape emitted by `azd ext list -o json`.
// Only the fields we need are decoded; the SDK adds extra fields freely.
type extListItem struct {
	ID               string `json:"id"`
	Namespace        string `json:"namespace"`
	InstalledVersion string `json:"installedVersion"`
}

// extLookup describes the install state of one sibling extension. The
// shape stays small on purpose -- callers only need to know "is it
// installed?" and "what's the namespace I'd invoke?" (for nicer error
// messages when the answer is no).
type extLookup struct {
	ID        string
	Namespace string
	Installed bool
}

// azdRunner abstracts the exec.Command wiring so tests can inject a
// fake. Default production runner is osAzdRunner below.
type azdRunner interface {
	// Run executes `azd <args...>` with the given stdout/stderr writers
	// and returns the process error (nil on exit 0). Cancellation is
	// honored when ctx is canceled.
	Run(ctx context.Context, args []string, stdout, stderr io.Writer) error
	// Output executes `azd <args...>` and returns combined stdout +
	// error (mirrors exec.Command.Output). Used by the JSON-parsing
	// helpers where streaming is not needed.
	Output(ctx context.Context, args []string) ([]byte, error)
}

// osAzdRunner is the default production runner.
type osAzdRunner struct{}

func (osAzdRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "azd", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (osAzdRunner) Output(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "azd", args...)
	return cmd.Output()
}

// lookupExtension returns the install state for the given extension ID.
// Returns (lookup, nil) when the listing succeeds and the ID is found;
// returns (lookup with Installed=false, nil) when listing succeeds and
// the ID is missing; returns (zero, err) when the listing itself fails.
func lookupExtension(ctx context.Context, runner azdRunner, id string) (extLookup, error) {
	out, err := runner.Output(ctx, []string{"ext", "list", "-o", "json"})
	if err != nil {
		return extLookup{}, fmt.Errorf("run `azd ext list -o json`: %w", err)
	}

	var items []extListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return extLookup{}, fmt.Errorf("parse `azd ext list` output: %w", err)
	}

	for _, it := range items {
		if !strings.EqualFold(it.ID, id) {
			continue
		}
		return extLookup{
			ID:        it.ID,
			Namespace: it.Namespace,
			Installed: strings.TrimSpace(it.InstalledVersion) != "",
		}, nil
	}

	// Not present in the catalog at all (no registry source advertises
	// it, or the user has not added the right source). Return a lookup
	// with Installed=false so the caller surfaces an "install it" hint.
	return extLookup{ID: id, Installed: false}, nil
}

// installExtension shells out to `azd ext install <id>`. Streams output
// through stdout/stderr so the user sees install progress live. Used
// when the user opts in to auto-installing a missing dependency.
func installExtension(ctx context.Context, runner azdRunner, id string, stdout, stderr io.Writer) error {
	args := []string{"ext", "install", id}
	if err := runner.Run(ctx, args, stdout, stderr); err != nil {
		return fmt.Errorf("install extension %q: %w", id, err)
	}
	return nil
}

// runChildAzd invokes `azd <args...>` with stdout/stderr streamed
// through. Returns the process error verbatim so the caller can pattern-
// match on exit codes / unwrap exec.ExitError when needed.
//
// Used by the init pre-flow to dispatch `azd ai doc skills install`.
// Always pass --no-prompt + --output json from the caller; this helper
// makes no assumption about flags so it can be reused for other
// cross-extension calls in the future.
func runChildAzd(ctx context.Context, runner azdRunner, args []string, stdout, stderr io.Writer) error {
	return runner.Run(ctx, args, stdout, stderr)
}

// defaultAzdRunner is the package-level production runner. Tests
// construct their own runner and call the *With helpers directly.
var defaultAzdRunner azdRunner = osAzdRunner{}
