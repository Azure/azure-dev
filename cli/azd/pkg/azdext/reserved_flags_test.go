// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newPlainRoot creates a plain cobra root command (no SDK persistent flags).
// This simulates an extension that doesn't use NewExtensionRootCommand.
func newPlainRoot(name string) *cobra.Command {
	return &cobra.Command{Use: name}
}

func TestValidateNoReservedFlagConflicts_Clean(t *testing.T) {
	root, _ := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	sub := &cobra.Command{Use: "do"}
	sub.Flags().StringP("endpoint", "p", "", "project endpoint")
	sub.Flags().String("subscription", "", "subscription id")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.NoError(t, err, "no conflict expected for flags that don't collide")
}

func TestValidateNoReservedFlagConflicts_ShortCollision(t *testing.T) {
	// Use a plain root because cobra panics if a child redefines a shorthand
	// already claimed by a parent's persistent flags (the SDK root has -e).
	root := newPlainRoot("test")
	sub := &cobra.Command{Use: "create"}
	sub.Flags().StringP("project-endpoint", "e", "", "project endpoint")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "short flag -e is reserved")
	require.Contains(t, err.Error(), "project-endpoint")
}

func TestValidateNoReservedFlagConflicts_LongCollision(t *testing.T) {
	root := newPlainRoot("test")
	sub := &cobra.Command{Use: "init"}
	sub.Flags().StringP("environment", "n", "", "environment name or ID")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "long flag --environment is reserved")
}

func TestValidateNoReservedFlagConflicts_RootFlagsAllowed(t *testing.T) {
	// The SDK's own root persistent flags (--environment, --debug, --cwd, etc.)
	// mirror azd globals intentionally and must be allowed.
	root, _ := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	sub := &cobra.Command{Use: "list"}
	sub.Flags().String("filter", "", "filter expression")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.NoError(t, err, "root persistent flags from SDK should be allowed")
}

func TestValidateNoReservedFlagConflicts_MultipleCollisions(t *testing.T) {
	root := newPlainRoot("test")
	sub := &cobra.Command{Use: "create"}
	sub.Flags().StringP("project-endpoint", "e", "", "endpoint")
	sub.Flags().StringP("output-format", "o", "", "output format")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "-e is reserved")
	require.Contains(t, err.Error(), "-o is reserved")
}

func TestValidateNoReservedFlagConflicts_NestedSubcommand(t *testing.T) {
	root := newPlainRoot("test")
	parent := &cobra.Command{Use: "model"}
	child := &cobra.Command{Use: "create"}
	child.Flags().StringP("project-endpoint", "e", "", "endpoint")
	parent.AddCommand(child)
	root.AddCommand(parent)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "model create")
	require.Contains(t, err.Error(), "-e is reserved")
}

func TestValidateNoReservedFlagConflicts_SDKRootWithCollision(t *testing.T) {
	// When using the SDK root, -C is already registered as a persistent flag.
	// A subcommand that uses -C for a different flag (via StringVar + Shorthand)
	// would be caught. We use a long-name collision instead to avoid cobra panic.
	root, _ := NewExtensionRootCommand(ExtensionCommandOptions{Name: "test"})
	sub := &cobra.Command{Use: "init"}
	// --docs collides with reserved long flag
	sub.Flags().Bool("docs", false, "generate docs")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "long flag --docs is reserved")
}

func TestValidateNoReservedFlagConflicts_PersistentFlagCollision(t *testing.T) {
	// A subcommand that defines a persistent flag colliding with a reserved name
	// should be caught even though it uses PersistentFlags() not Flags().
	root := newPlainRoot("test")
	sub := &cobra.Command{Use: "deploy"}
	sub.PersistentFlags().Bool("debug", false, "extension debug mode")
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "long flag --debug is reserved")
}

func TestReservedFlagNames(t *testing.T) {
	names := ReservedFlagNames()
	require.NotEmpty(t, names)
	require.Contains(t, names, "environment")
	require.Contains(t, names, "cwd")
	require.Contains(t, names, "debug")
	require.Contains(t, names, "output")
	require.Contains(t, names, "help")
}

func TestReservedFlagsInSyncWithInternal(t *testing.T) {
	// Verify the SDK reserved flag list stays in sync with the internal registry.
	// If this test fails, you added a flag to one list but not the other.
	sdkFlags := reservedGlobalFlags
	internalFlags := internal.ReservedFlags()

	// Build maps of long name -> short name for both sides.
	sdkMap := make(map[string]string, len(sdkFlags))
	for _, f := range sdkFlags {
		sdkMap[f.Long] = f.Short
	}

	internalMap := make(map[string]string, len(internalFlags))
	for _, f := range internalFlags {
		internalMap[f.Long] = f.Short
	}

	// Check every SDK flag exists in internal with matching short name.
	for long, short := range sdkMap {
		internalShort, ok := internalMap[long]
		require.True(t, ok,
			"azdext reserved_flags.go has %q but internal.ReservedFlags() does not — add it to internal/reserved_flags.go",
			long)
		require.Equal(t, internalShort, short,
			"short name mismatch for %q: internal has %q, SDK has %q",
			long, internalShort, short)
	}

	// Check every internal flag exists in SDK with matching short name.
	for long, short := range internalMap {
		sdkShort, ok := sdkMap[long]
		require.True(t, ok,
			"internal.ReservedFlags() has %q but azdext reserved_flags.go does not — add it to pkg/azdext/reserved_flags.go",
			long)
		require.Equal(t, sdkShort, short,
			"short name mismatch for %q: SDK has %q, internal has %q",
			long, sdkShort, short)
	}
}

func TestValidateNoReservedFlagConflicts_NonSDKRootPersistentFlag(t *testing.T) {
	// An extension that manually adds a root persistent flag colliding with a
	// reserved global (not via the SDK) should still be caught. This verifies
	// the tightened exemption: only known SDK globals are exempt, not arbitrary
	// root persistent flags.
	root := newPlainRoot("test")
	root.PersistentFlags().StringP("environment", "e", "", "custom env flag")
	sub := &cobra.Command{Use: "run"}
	root.AddCommand(sub)

	err := ValidateNoReservedFlagConflicts(root)
	require.Error(t, err)
	require.Contains(t, err.Error(), "long flag --environment is reserved")
}
