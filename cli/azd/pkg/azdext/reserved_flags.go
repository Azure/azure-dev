// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// reservedFlag describes a global flag owned by azd that extensions must not reuse
// for a different purpose.
type reservedFlag struct {
	Long  string
	Short string
}

// reservedGlobalFlags is the canonical list of global flags that extensions must not reuse.
// Keep this in sync with internal.ReservedFlags and CreateGlobalFlagSet (auto_install.go).
var reservedGlobalFlags = []reservedFlag{
	{Long: "environment", Short: "e"},
	{Long: "cwd", Short: "C"},
	{Long: "debug", Short: ""},
	{Long: "no-prompt", Short: ""},
	{Long: "output", Short: "o"},
	{Long: "help", Short: "h"},
	{Long: "docs", Short: ""},
	{Long: "trace-log-file", Short: ""},
	{Long: "trace-log-url", Short: ""},
}

// reservedShorts is an index of short flag names built once at initialization time.
var reservedShorts = func() map[string]string {
	m := make(map[string]string, len(reservedGlobalFlags))
	for _, f := range reservedGlobalFlags {
		if f.Short != "" {
			m[f.Short] = f.Long
		}
	}
	return m
}()

// reservedLongs is an index of long flag names built once at initialization time.
var reservedLongs = func() map[string]bool {
	m := make(map[string]bool, len(reservedGlobalFlags))
	for _, f := range reservedGlobalFlags {
		m[f.Long] = true
	}
	return m
}()

// ReservedFlagNames returns the long names of all reserved global flags.
// This is intended for documentation and error messages.
func ReservedFlagNames() []string {
	names := make([]string, len(reservedGlobalFlags))
	for i, f := range reservedGlobalFlags {
		names[i] = f.Long
	}
	return names
}

// FlagConflict describes a single flag that conflicts with a reserved azd global flag.
type FlagConflict struct {
	// Command is the full command path (e.g. "model custom create").
	Command string
	// FlagName is the long name of the conflicting flag.
	FlagName string
	// FlagShort is the short name of the conflicting flag (may be empty).
	FlagShort string
	// ReservedLong is the long name of the reserved flag it conflicts with.
	ReservedLong string
	// Reason describes why it conflicts (e.g. "short flag -e is reserved").
	Reason string
}

func (c FlagConflict) String() string {
	return fmt.Sprintf("command %q: flag %s conflicts with reserved global flag --%s (%s)",
		c.Command, c.flagDisplay(), c.ReservedLong, c.Reason)
}

func (c FlagConflict) flagDisplay() string {
	if c.FlagShort != "" {
		return fmt.Sprintf("--%s/-%s", c.FlagName, c.FlagShort)
	}
	return fmt.Sprintf("--%s", c.FlagName)
}

// ValidateNoReservedFlagConflicts walks the command tree rooted at cmd and
// returns an error listing every extension-defined flag that collides with an
// azd reserved global flag.
//
// Flags registered on the root command's persistent flag set are allowed because
// the extension SDK intentionally mirrors azd's global flags there (see
// NewExtensionRootCommand). Only flags added by the extension on subcommands
// (local or inherited persistent flags not from root) are checked.
func ValidateNoReservedFlagConflicts(root *cobra.Command) error {
	conflicts := collectConflicts(root, root)
	if len(conflicts) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("extension defines flags that conflict with reserved azd global flags:\n")
	for _, c := range conflicts {
		b.WriteString("  - ")
		b.WriteString(c.String())
		b.WriteString("\n")
	}
	b.WriteString("Remove or rename these flags to avoid conflicts with azd's global flags.\n")
	b.WriteString("Reserved flags: ")
	b.WriteString(strings.Join(ReservedFlagNames(), ", "))
	return fmt.Errorf("%s", b.String())
}

// collectConflicts recursively collects flag conflicts from the command tree.
func collectConflicts(root, cmd *cobra.Command) []FlagConflict {
	var conflicts []FlagConflict

	// Track which flags we've already checked to avoid duplicate reports
	// when a flag appears in both Flags() and PersistentFlags().
	checked := make(map[string]struct{})

	checkFlagOnce := func(f *pflag.Flag) {
		if _, seen := checked[f.Name]; seen {
			return
		}
		checked[f.Name] = struct{}{}

		// Skip only SDK-provided root persistent flags. The annotation check ensures that
		// extensions using a plain root (not NewExtensionRootCommand) that manually add
		// root persistent flags colliding with reserved globals are still caught.
		if root.Annotations["azd-sdk-root"] == "true" {
			if rootFlag := root.PersistentFlags().Lookup(f.Name); rootFlag != nil && rootFlag == f {
				return
			}
		}

		conflicts = append(conflicts, checkFlag(cmd, f)...)
	}

	// Check flags defined directly on this command (not inherited from parents).
	// We use cmd.Flags() instead of cmd.LocalFlags() because LocalFlags()
	// triggers cobra's mergePersistentFlags, which panics on shorthand conflicts.
	cmd.Flags().VisitAll(checkFlagOnce)

	// Also check persistent flags defined on this command. This catches cases where
	// an extension defines a persistent flag (e.g. on a subcommand) that conflicts
	// with a reserved flag but wouldn't appear in cmd.Flags().
	cmd.PersistentFlags().VisitAll(checkFlagOnce)

	// Recurse into subcommands.
	for _, sub := range cmd.Commands() {
		conflicts = append(conflicts, collectConflicts(root, sub)...)
	}

	return conflicts
}

// checkFlag checks a single flag against the reserved lists and returns all
// conflicts found (short-name and long-name may both collide).
func checkFlag(cmd *cobra.Command, f *pflag.Flag) []FlagConflict {
	cmdPath := commandPath(cmd)
	var conflicts []FlagConflict

	// Check short flag collision.
	if f.Shorthand != "" {
		if reservedLong, ok := reservedShorts[f.Shorthand]; ok {
			conflicts = append(conflicts, FlagConflict{
				Command:      cmdPath,
				FlagName:     f.Name,
				FlagShort:    f.Shorthand,
				ReservedLong: reservedLong,
				Reason:       fmt.Sprintf("short flag -%s is reserved by azd for --%s", f.Shorthand, reservedLong),
			})
		}
	}

	// Check long flag collision (only if the long name is the same but used for
	// a different purpose — identified by being on a subcommand, not root).
	if reservedLongs[f.Name] {
		conflicts = append(conflicts, FlagConflict{
			Command:      cmdPath,
			FlagName:     f.Name,
			FlagShort:    f.Shorthand,
			ReservedLong: f.Name,
			Reason:       fmt.Sprintf("long flag --%s is reserved by azd", f.Name),
		})
	}

	return conflicts
}

// commandPath returns the space-separated command path (excluding root).
func commandPath(cmd *cobra.Command) string {
	var parts []string
	for c := cmd; c != nil && c.HasParent(); c = c.Parent() {
		parts = append(parts, c.Name())
	}
	slices.Reverse(parts)
	return strings.Join(parts, " ")
}
