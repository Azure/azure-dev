// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

func TestNonGoScaffoldIncludesStructuredErrorProtocol(t *testing.T) {
	errorsProto, err := Languages.ReadFile("languages/proto/errors.proto")
	require.NoError(t, err)
	require.Contains(t, string(errorsProto), "message ExtensionError")
	require.NotContains(t, string(errorsProto), "cause_types")
	require.NotContains(t, string(errorsProto), "ToolErrorDetail")

	eventProto, err := Languages.ReadFile("languages/proto/event.proto")
	require.NoError(t, err)
	eventContents := string(eventProto)
	require.Contains(t, eventContents, `import "errors.proto";`)
	require.Contains(t, eventContents, "ExtensionError error = 4;")
	require.Contains(t, eventContents, "ExtensionError error = 5;")
}

func TestNonGoScaffoldGeneratedErrorsLoadWithEvents(t *testing.T) {
	for _, path := range []string{
		"languages/javascript/generated/proto/errors_pb.js",
		"languages/python/generated_proto/errors_pb2.py",
	} {
		_, err := Languages.ReadFile(path)
		require.NoError(t, err)
	}

	t.Run("javascript", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skip("Node.js is not installed")
		}
		if err := exec.Command(node, "-e", "require('google-protobuf')").Run(); err != nil {
			t.Skip("google-protobuf is not installed")
		}

		script := `
const errors = require('./languages/javascript/generated/proto/errors_pb.js');
const events = require('./languages/javascript/generated/proto/event_pb.js');
const status = new events.ProjectHandlerStatus();
status.setError(new errors.ExtensionError().setMessage('failed'));
const decoded = events.ProjectHandlerStatus.deserializeBinary(status.serializeBinary());
if (decoded.getError().getMessage() !== 'failed') process.exit(1);
`
		output, err := exec.Command(node, "-e", script).CombinedOutput()
		require.NoError(t, err, string(output))
	})

	t.Run("python", func(t *testing.T) {
		var python string
		for _, name := range []string{"python3", "python"} {
			path, err := exec.LookPath(name)
			if err == nil && exec.Command(path, "-c", "import google.protobuf").Run() == nil {
				python = path
				break
			}
		}
		if python == "" {
			t.Skip("protobuf is not installed")
		}

		script := `
import sys
sys.path.insert(0, 'languages/python/generated_proto')
import errors_pb2
import event_pb2
status = event_pb2.ProjectHandlerStatus(
    error=errors_pb2.ExtensionError(message='failed'))
decoded = event_pb2.ProjectHandlerStatus.FromString(status.SerializeToString())
assert decoded.error.message == 'failed'
`
		output, err := exec.Command(python, "-c", script).CombinedOutput()
		require.NoError(t, err, string(output))
	})
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
				require.NotContains(
					t,
					strings.ToLower(path),
					"compose",
					"Compose is beta-only and not part of stable scaffolds",
				)

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

func TestNonGoScaffoldEventMessageMatchesStableContract(t *testing.T) {
	scaffold, err := Languages.ReadFile("languages/proto/event.proto")
	require.NoError(t, err)

	canonical, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "..", "grpc", "proto", "azd", "extensions", "v1", "event.proto",
	))
	require.NoError(t, err)

	require.Equal(t, eventMessageFields(t, canonical), eventMessageFields(t, scaffold))

	for _, path := range []string{
		"languages/javascript/generated/proto/event_pb.js",
		"languages/python/generated_proto/event_pb2.py",
	} {
		generated, err := Languages.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(generated), "ExtensionReadyEvent", path)
		require.NotContains(t, string(generated), "extension_ready_event", path)
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

func eventMessageFields(t *testing.T, proto []byte) map[string]string {
	t.Helper()

	oneof := eventMessageOneofPattern.FindSubmatch(proto)
	require.Len(t, oneof, 2, "EventMessage message_type oneof must be present")

	fields := map[string]string{}
	for _, match := range protoFieldPattern.FindAllSubmatch(oneof[1], -1) {
		fields[string(match[2])] = string(match[1]) + ":" + string(match[3])
	}
	require.NotEmpty(t, fields, "EventMessage message_type oneof must contain fields")
	return fields
}

var azdModuleRequirePattern = regexp.MustCompile(`github\.com/azure/azure-dev/cli/azd (\S+)`)
var eventMessageOneofPattern = regexp.MustCompile(`(?s)message EventMessage\s*\{.*?oneof message_type\s*\{(.*?)\n\s*\}`)
var protoFieldPattern = regexp.MustCompile(`(?m)^\s*([\w.]+)\s+(\w+)\s*=\s*(\d+);`)

// pseudoVersionPattern matches the trailing "<yyyymmddhhmmss>-<12 hex digits>" that the Go
// toolchain appends when a module is referenced by commit rather than by a published tag.
var pseudoVersionPattern = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)
