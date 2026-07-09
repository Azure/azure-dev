// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/spf13/cobra"
)

// fromVersionFlagHelp is the shared --from-version help text for the
// skill/connection add and remove verbs.
const fromVersionFlagHelp = `Version to branch the new version from. Defaults to the numerically ` +
	`latest existing version (same as passing "latest") so sequential add/remove ` +
	`calls chain onto each other instead of silently forking from a stale default. ` +
	`Pass "default" to branch from the toolbox's current default version instead, ` +
	`or an explicit version number to fork from a specific point in history.`

// registerFromVersionFlag attaches the --from-version flag shared by the
// skill/connection add and remove verbs.
func registerFromVersionFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "from-version", "", fromVersionFlagHelp)
}

// resolveBaseVersion determines which existing toolbox version a skill or
// connection mutation should branch its new version from.
//
// fromVersion is normally sourced from the verb's --from-version flag:
//   - "" or "latest": the numerically highest existing version. This is the
//     default so sequential add/remove calls chain onto each other instead of
//     silently forking from a stale default version (see azure-dev#9034).
//   - "default": the toolbox's current default version — the pre-fix
//     behavior, kept reachable for callers that deliberately want to fork
//     from what's currently published rather than the newest version.
//   - anything else: used verbatim as the version to fetch. An invalid value
//     surfaces as a "version not found" error from the caller's subsequent
//     GetToolboxVersion call.
func resolveBaseVersion(
	ctx context.Context, client toolboxClient, toolboxName string,
	tb *azure.ToolboxObject, fromVersion string,
) (string, error) {
	switch strings.ToLower(strings.TrimSpace(fromVersion)) {
	case "", "latest":
		return latestToolboxVersion(ctx, client, toolboxName, tb.DefaultVersion)
	case "default":
		return tb.DefaultVersion, nil
	default:
		return strings.TrimSpace(fromVersion), nil
	}
}

// latestToolboxVersion returns the numerically highest version among
// toolboxName's published versions. Falls back to `fallback` (normally the
// toolbox's default version) when the listing comes back empty, which should
// not happen for a toolbox that exists but keeps this defensive.
func latestToolboxVersion(
	ctx context.Context, client toolboxClient, toolboxName, fallback string,
) (string, error) {
	versions, err := client.ListToolboxVersions(ctx, toolboxName)
	if err != nil {
		return "", exterrors.ServiceFromAzure(err, exterrors.OpListToolboxVersions)
	}
	if len(versions) == 0 {
		return fallback, nil
	}
	// Same ordering as `toolbox versions list`: numeric descending, lexical
	// descending fallback. versions[0] after sorting is the latest.
	slices.SortFunc(versions, func(a, b azure.ToolboxVersionObject) int {
		return versionSortDescending(a.Version, b.Version)
	})
	return versions[0].Version, nil
}
