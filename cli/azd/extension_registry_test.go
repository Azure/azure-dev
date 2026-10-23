// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestValidateExtensionAzdVersions(t *testing.T) {
	release := func(version, requirement string) extensions.ExtensionVersion {
		return extensions.ExtensionVersion{Version: version, RequiredAzdVersion: requirement}
	}
	registry := func(versions ...extensions.ExtensionVersion) *extensions.Registry {
		return &extensions.Registry{Extensions: []*extensions.ExtensionMetadata{
			{Id: "test.extension", Versions: versions},
		}}
	}
	tests := []struct {
		name      string
		base      *extensions.Registry
		proposed  *extensions.Registry
		published string
		wantError string
	}{
		{
			name:      "new extension requiring unpublished core",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.37.0")),
			published: "1.36.0",
			wantError: `[test.extension@1.0.0] requiredAzdVersion ">=1.37.0"`,
		},
		{
			name:      "new version requiring unpublished core",
			base:      registry(release("1.0.0", ">=1.35.0")),
			proposed:  registry(release("1.0.0", ">=1.35.0"), release("1.1.0", ">=1.37.0")),
			published: "1.36.0",
			wantError: "[test.extension@1.1.0]",
		},
		{
			name:      "modified requirement",
			base:      registry(release("1.0.0", ">=1.35.0")),
			proposed:  registry(release("1.0.0", ">=1.37.0")),
			published: "1.36.0",
			wantError: "[test.extension@1.0.0]",
		},
		{
			name:      "passes once required core is published",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.37.0")),
			published: "1.37.0",
		},
		{
			name:      "older requirement",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.24.0")),
			published: "1.36.0",
		},
		{
			name:      "compound constraint",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.24.0, <2.0.0")),
			published: "1.36.0",
		},
		{
			name:      "unsatisfied upper bound",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.24.0, <1.36.0")),
			published: "1.36.0",
			wantError: "not satisfied by latest published azd 1.36.0",
		},
		{
			name:      "alternative constraint",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", "^1.36.0 || >=2.0.0")),
			published: "1.36.0",
		},
		{
			name:      "missing requirement",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", "")),
			published: "1.36.0",
		},
		{
			name:      "invalid requirement fails closed",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", "invalid")),
			published: "1.36.0",
			wantError: `invalid requiredAzdVersion "invalid"`,
		},
		{
			name:      "unchanged incompatible version",
			base:      registry(release("1.0.0", ">=1.37.0")),
			proposed:  registry(release("1.0.0", ">=1.37.0")),
			published: "1.36.0",
		},
		{
			name:      "unchanged invalid requirement",
			base:      registry(release("1.0.0", "invalid")),
			proposed:  registry(release("1.0.0", "invalid")),
			published: "1.36.0",
		},
		{
			name:      "removed incompatible version",
			base:      registry(release("1.0.0", ">=1.37.0")),
			proposed:  registry(),
			published: "1.36.0",
		},
		{
			name:      "unchanged version does not block compatible addition",
			base:      registry(release("1.0.0", "<1.36.0")),
			proposed:  registry(release("1.0.0", "<1.36.0"), release("1.1.0", ">=1.36.0")),
			published: "1.36.0",
		},
		{
			name:      "prerelease core does not satisfy stable requirement",
			base:      &extensions.Registry{},
			proposed:  registry(release("1.0.0", ">=1.37.0")),
			published: "1.37.0-beta.1",
			wantError: "not satisfied by latest published azd 1.37.0-beta.1",
		},
		{
			name:      "null extension fails closed",
			base:      &extensions.Registry{},
			proposed:  &extensions.Registry{Extensions: []*extensions.ExtensionMetadata{nil}},
			published: "1.36.0",
			wantError: "proposed registry contains a null extension",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExtensionAzdVersions(tt.base, tt.proposed, semver.MustParse(tt.published))
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("artifact changes revalidate existing version", func(t *testing.T) {
		base := registry(release("1.0.0", ">=1.37.0"))
		proposed := registry(release("1.0.0", ">=1.37.0"))
		proposed.Extensions[0].Versions[0].Artifacts = map[string]extensions.ExtensionArtifact{
			"linux/amd64": {URL: "https://example.com/extension"},
		}
		require.ErrorContains(t, validateExtensionAzdVersions(base, proposed, semver.MustParse("1.36.0")),
			"[test.extension@1.0.0]")
	})

	t.Run("extension metadata changes do not revalidate versions", func(t *testing.T) {
		base := registry(release("1.0.0", ">=1.37.0"))
		proposed := registry(release("1.0.0", ">=1.37.0"))
		proposed.Extensions[0].Description = "Updated description"
		require.NoError(t, validateExtensionAzdVersions(base, proposed, semver.MustParse("1.36.0")))
	})
}

func TestCheckExtensionAzdVersionsRejectsInvalidPublishedVersion(t *testing.T) {
	for _, version := range []string{"", " \n", "not-a-version", "<html>unavailable</html>"} {
		t.Run(version, func(t *testing.T) {
			require.ErrorContains(t, CheckExtensionAzdVersions(t.Context(), "HEAD", version),
				"invalid published azd version")
		})
	}
}

func TestCheckExtensionAzdVersions(t *testing.T) {
	repoRoot := initTempRepo(t)
	t.Chdir(repoRoot)
	registryDir := filepath.Join(repoRoot, "cli", "azd", "extensions")
	require.NoError(t, os.MkdirAll(registryDir, 0o755))
	names := []string{"registry.json", "registry.dev.json"}
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(registryDir, name), []byte(`{"extensions":[]}`), 0o600))
	}
	require.NoError(t, exec.CommandContext(t.Context(), "git", "add", ".").Run())
	tree, err := exec.CommandContext(t.Context(), "git", "write-tree").Output()
	require.NoError(t, err)
	baseRef := strings.TrimSpace(string(tree))

	proposedData := []byte(`{
		"extensions": [{
			"id": "test.extension",
			"versions": [{"version": "1.0.0", "requiredAzdVersion": ">=1.37.0"}]
		}]
	}`)
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(registryDir, name), proposedData, 0o600))
	}

	err = CheckExtensionAzdVersions(t.Context(), baseRef, "1.36.0")
	require.ErrorContains(t, err, "registry.json: [test.extension@1.0.0]")
	require.ErrorContains(t, err, "registry.dev.json: [test.extension@1.0.0]")
	require.ErrorContains(t, err, "latest published azd 1.36.0")
	require.ErrorContains(t, err, "rerun ext-registry-ci")
	require.NoError(t, CheckExtensionAzdVersions(t.Context(), baseRef, " 1.37.0\n"))

	require.ErrorContains(t, CheckExtensionAzdVersions(t.Context(), "missing-ref", "1.37.0"),
		"reading cli/azd/extensions/registry.json at missing-ref")

	for _, name := range names {
		t.Run("invalid JSON in "+name, func(t *testing.T) {
			path := filepath.Join(registryDir, name)
			t.Cleanup(func() {
				require.NoError(t, os.WriteFile(path, proposedData, 0o600))
			})
			require.NoError(t, os.WriteFile(path, []byte("{invalid"), 0o600))
			require.ErrorContains(t, CheckExtensionAzdVersions(t.Context(), baseRef, "1.37.0"),
				"parsing cli/azd/extensions/"+name)
		})
	}
}
