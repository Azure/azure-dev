// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/spf13/cobra"
)

// branchResolution describes which existing toolbox version a mutation
// (skill/connection add or remove) should branch its new immutable version from.
type branchResolution struct {
	// Branch is the version the new version is created from.
	Branch string
	// Latest is the highest existing version of the toolbox.
	Latest string
	// Default is the toolbox's current default version.
	Default string
}

// branchedFromNonDefault reports whether the new version was branched from a
// version other than the toolbox's default. When true, the default version's
// contents are NOT the base of the new version, which is worth surfacing to the
// user (the new version accumulates on top of Latest, not Default).
func (b branchResolution) branchedFromNonDefault() bool {
	return b.Branch != b.Default
}

// registerFromVersionFlag registers the shared --from-version flag used by the
// add/remove verbs to override which version a new version branches from.
func registerFromVersionFlag(cmd *cobra.Command, fromVersion *string) {
	cmd.Flags().StringVar(
		fromVersion, "from-version", "",
		"Version to branch the new version from (defaults to the latest version).",
	)
}

// resolveBranchVersion determines which existing toolbox version a new version
// should branch from.
//
// By default it branches from the toolbox's LATEST version so sequential
// add/remove operations accumulate (v3 = v2 + change), rather than repeatedly
// forking from the default version and silently dropping earlier changes.
// An explicit --from-version overrides this (the prior default-snapshot behavior
// is still reachable via --from-version <default>).
//
// "Latest" is the most recently created version: the toolbox API exposes no
// canonical latest/head pointer (ToolboxObject carries only default_version), so
// we derive it from the version list, preferring the newest CreatedAt and
// falling back to the highest version number when CreatedAt is unavailable or
// tied. Version numbers are monotonic, so this matches the true tip even if a
// version is later deleted.
//
// When the toolbox reports no versions (an edge case; a real toolbox always has
// its default), it falls back to the default version so the operation still
// succeeds. A --from-version that does not exist is rejected.
func resolveBranchVersion(
	ctx context.Context,
	client toolboxClient,
	toolboxName string,
	tb *azure.ToolboxObject,
	fromVersion string,
) (branchResolution, error) {
	versions, err := client.ListToolboxVersions(ctx, toolboxName)
	if err != nil {
		return branchResolution{}, exterrors.ServiceFromAzure(err, exterrors.OpListToolboxVersions)
	}

	latest := tb.DefaultVersion
	if len(versions) > 0 {
		sorted := slices.Clone(versions)
		slices.SortFunc(sorted, latestFirst)
		latest = sorted[0].Version
	}

	res := branchResolution{Branch: latest, Latest: latest, Default: tb.DefaultVersion}

	fromVersion = strings.TrimSpace(fromVersion)
	if fromVersion == "" {
		return res, nil
	}

	versionExists := fromVersion == tb.DefaultVersion
	if len(versions) > 0 {
		versionExists = slices.ContainsFunc(versions, func(v azure.ToolboxVersionObject) bool {
			return v.Version == fromVersion
		})
	}
	if !versionExists {
		return branchResolution{}, exterrors.Validation(
			exterrors.CodeToolboxVersionNotFound,
			fmt.Sprintf("version %q does not exist for toolbox %q", fromVersion, toolboxName),
			fmt.Sprintf("run `azd ai toolbox versions list %q` to see available versions", toolboxName),
		)
	}

	res.Branch = fromVersion
	return res, nil
}

// latestFirst orders two toolbox versions newest-first: by CreatedAt descending,
// then by the numeric-aware version comparator as a deterministic tiebreaker
// (and the sole signal when CreatedAt is not populated).
func latestFirst(a, b azure.ToolboxVersionObject) int {
	if a.CreatedAt != b.CreatedAt {
		return cmp.Compare(b.CreatedAt, a.CreatedAt)
	}
	return versionSortDescending(a.Version, b.Version)
}

// printBranchNote surfaces, in text output only, that a new version was branched
// from a version other than the default so the user understands the new version
// extends Latest (not Default) and the default is still unchanged. It is a no-op
// when the branch is the default version or in JSON output.
func printBranchNote(output string, branch branchResolution) {
	if output == "json" || !branch.branchedFromNonDefault() {
		return
	}
	if branch.Branch == branch.Latest {
		fmt.Printf(
			"Note: branched from version %s (the latest); the default version is still %s.\n",
			branch.Branch, branch.Default,
		)
		return
	}
	fmt.Printf(
		"Note: branched from version %s (--from-version); the latest is %s and the default is %s.\n",
		branch.Branch, branch.Latest, branch.Default,
	)
}
