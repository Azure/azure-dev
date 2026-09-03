// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

// TestGitignoreEmbedded verifies that the dotfiles (.gitignore) shipped with each
// language template are embedded. Without the `all:` prefix on the go:embed
// directive these files are silently skipped, which previously meant generated
// extensions had no .gitignore (so build artifacts under bin/ could be committed).
func TestGitignoreEmbedded(t *testing.T) {
	for _, language := range []string{"go", "dotnet", "javascript", "python"} {
		t.Run(language, func(t *testing.T) {
			contents, err := Languages.ReadFile("languages/" + language + "/.gitignore")
			require.NoError(t, err)
			require.NotEmpty(t, contents)
		})
	}
}

// TestGoGitignoreExcludesBin ensures the generated Go extension ignores the build
// output directory so binaries are not accidentally committed.
func TestGoGitignoreExcludesBin(t *testing.T) {
	contents, err := Languages.ReadFile("languages/go/.gitignore")
	require.NoError(t, err)
	require.Contains(t, string(contents), "bin/")
}

// goScaffoldVerbatimFiles are shipped verbatim (or nearly so) into generated Go
// extension projects, where Go tooling always writes LF. Committing them with CRLF
// leaves generated projects with mixed line endings that `go mod tidy` immediately
// rewrites, producing spurious diffs on the developer's first build.
var goScaffoldVerbatimFiles = []string{
	"languages/go/go.mod.tmpl",
	"languages/go/go.sum",
}

func TestGoScaffoldModuleFilesUseLFLineEndings(t *testing.T) {
	for _, name := range goScaffoldVerbatimFiles {
		t.Run(name, func(t *testing.T) {
			contents, err := Languages.ReadFile(name)
			require.NoError(t, err)
			require.NotContains(t, string(contents), "\r", "%s must use LF line endings", name)
		})
	}
}

// TestGoScaffoldPinsReleasedAzdModule guards against the generated go.mod pointing at
// an unreleased pseudo-version or a local replace directive, either of which breaks
// `go build` for anyone scaffolding an extension outside this repository.
func TestGoScaffoldPinsReleasedAzdModule(t *testing.T) {
	contents, err := Languages.ReadFile("languages/go/go.mod.tmpl")
	require.NoError(t, err)

	goMod := string(contents)
	require.NotContains(t, goMod, "replace ", "the scaffolded go.mod must not contain replace directives")

	match := azdModuleRequirePattern.FindStringSubmatch(goMod)
	require.NotNil(t, match, "the scaffolded go.mod must require github.com/azure/azure-dev/cli/azd")

	version := match[1]
	require.Regexpf(t,
		`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`,
		version,
		"the scaffolded go.mod must pin a released cli/azd module tag, got %q",
		version,
	)
	require.NotRegexpf(t,
		pseudoVersionPattern,
		version,
		"the scaffolded go.mod must pin a released cli/azd module tag, not a pseudo-version, got %q",
		version,
	)
}

func TestNonGoScaffoldsUseVersionedGrpcPackages(t *testing.T) {
	roots := []string{
		"languages/proto",
		"languages/javascript/generated/proto",
		"languages/python/generated_proto",
	}

	for _, root := range roots {
		t.Run(root, func(t *testing.T) {
			err := fs.WalkDir(Languages, root, func(path string, entry fs.DirEntry, err error) error {
				require.NoError(t, err)
				if entry.IsDir() {
					return nil
				}

				contents, err := Languages.ReadFile(path)
				require.NoError(t, err)
				text := string(contents)
				require.NotContains(t, text, "package azdext;", path)
				require.NotContains(t, text, "/azdext.", path)
				require.NotContains(t, text, "proto.azdext", path)
				require.NotContains(t, strings.ToLower(path), "compose", "Compose is beta-only and not part of stable scaffolds")

				if (strings.HasSuffix(path, "_grpc_pb.js") || strings.HasSuffix(path, "_pb2_grpc.py")) &&
					!strings.Contains(path, "/models_") {
					require.Contains(t, text, "/azd.extensions.v1.", path)
				}
				return nil
			})
			require.NoError(t, err)
		})
	}
}

func TestGoScaffoldReadmeMatchesCapabilities(t *testing.T) {
	contents, err := Languages.ReadFile("languages/go/README.md.tmpl")
	require.NoError(t, err)

	tests := []struct {
		name               string
		hasCustomCommands  bool
		hasLifecycleEvents bool
		contains           []string
		notContains        []string
	}{
		{
			name:              "custom commands",
			hasCustomCommands: true,
			contains:          []string{"## Commands", "### `context`", "### `prompt`"},
			notContains:       []string{"hidden `listen` command"},
		},
		{
			name:               "lifecycle events",
			hasLifecycleEvents: true,
			contains:           []string{"hidden `listen` command", "internal/cmd/listen.go"},
			notContains:        []string{"## Commands", "### `context`", "### `prompt`"},
		},
		{
			name:        "provider only",
			notContains: []string{"## Commands", "### `context`", "### `prompt`", "hidden `listen` command"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpl, err := template.New("README.md").Parse(string(contents))
			require.NoError(t, err)

			var rendered bytes.Buffer
			err = tmpl.Execute(&rendered, struct {
				Metadata struct {
					DisplayName string
					Description string
					Id          string
					Usage       string
				}
				HasCustomCommands  bool
				HasLifecycleEvents bool
			}{
				Metadata: struct {
					DisplayName string
					Description string
					Id          string
					Usage       string
				}{
					DisplayName: "Test Extension",
					Description: "Test description",
					Id:          "test.extension",
					Usage:       "azd test <command>",
				},
				HasCustomCommands:  test.hasCustomCommands,
				HasLifecycleEvents: test.hasLifecycleEvents,
			})
			require.NoError(t, err)

			for _, expected := range test.contains {
				require.Contains(t, rendered.String(), expected)
			}
			for _, unexpected := range test.notContains {
				require.NotContains(t, rendered.String(), unexpected)
			}
		})
	}
}

var azdModuleRequirePattern = regexp.MustCompile(`github\.com/azure/azure-dev/cli/azd (\S+)`)

// pseudoVersionPattern matches the trailing "<yyyymmddhhmmss>-<12 hex digits>" that the Go
// toolchain appends when a module is referenced by commit rather than by a published tag.
var pseudoVersionPattern = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)
