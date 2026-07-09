// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const providerVerificationTestName = "TestConfigureExtensionHostMatchesManifest"

type manifestProviders struct {
	Id        string     `yaml:"id"`
	Providers []Provider `yaml:"providers"`
	path      string
}

func loadFirstPartyManifests(t *testing.T) []manifestProviders {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join("..", "..", "extensions", "*", "extension.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected to find first-party extension manifests")

	manifests := make([]manifestProviders, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		require.NoErrorf(t, err, "reading %s", path)

		var manifest manifestProviders
		require.NoErrorf(t, yaml.Unmarshal(data, &manifest), "parsing %s", path)
		require.NotEmptyf(t, manifest.Id, "manifest %s is missing the required 'id' field", path)
		manifest.path = path
		manifests = append(manifests, manifest)
	}

	return manifests
}

// providerVerificationAllowlist lists provider-bearing extensions that cannot yet
// use VerifyProvidersMatchManifest because they pin an older published azd SDK.
var providerVerificationAllowlist = map[string]string{
	"azure.ai.projects": "pins a published azd SDK without azdext.VerifyProvidersMatchManifest",
	"azure.ai.skills":   "pins a published azd SDK without azdext.VerifyProvidersMatchManifest",
}

// TestExtensionManifestsHaveProviderVerificationTests ensures every first-party
// provider extension carries the canonical runtime registration verification test.
func TestExtensionManifestsHaveProviderVerificationTests(t *testing.T) {
	for _, manifest := range loadFirstPartyManifests(t) {
		reason, allowlisted := providerVerificationAllowlist[manifest.Id]

		t.Run(manifest.Id, func(t *testing.T) {
			testPath := filepath.Join(
				filepath.Dir(manifest.path),
				"internal",
				"cmd",
				"providers_manifest_test.go",
			)
			hasTest := hasCanonicalProviderVerificationTest(t, testPath)

			switch {
			case len(manifest.Providers) == 0:
				require.Falsef(t, allowlisted,
					"%s no longer declares providers; remove it from providerVerificationAllowlist",
					manifest.Id)
			case allowlisted:
				require.Falsef(t, hasTest,
					"%s now has a provider verification test; remove it from providerVerificationAllowlist",
					manifest.Id)
				t.Skipf("allowlisted: %s", reason)
			default:
				require.Truef(t, hasTest,
					"extension %s declares providers in %s but %s does not define %s asserting "+
						"azdext.VerifyProvidersMatchManifest with require.NoError",
					manifest.Id, manifest.path, testPath, providerVerificationTestName)
			}
		})
	}
}

func hasCanonicalProviderVerificationTest(t *testing.T, testPath string) bool {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), testPath, nil, 0)
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != providerVerificationTestName || function.Body == nil {
			continue
		}

		found := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			if isAssertedProviderVerificationCall(call) {
				found = true
				return false
			}
			return true
		})
		return found
	}

	return false
}

func isAssertedProviderVerificationCall(call *ast.CallExpr) bool {
	requireCall, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || requireCall.Sel.Name != "NoError" || len(call.Args) < 2 {
		return false
	}
	requirePackage, ok := requireCall.X.(*ast.Ident)
	if !ok || requirePackage.Name != "require" {
		return false
	}

	verificationCall, ok := call.Args[1].(*ast.CallExpr)
	if !ok {
		return false
	}
	verificationFunction, ok := verificationCall.Fun.(*ast.SelectorExpr)
	if !ok || verificationFunction.Sel.Name != "VerifyProvidersMatchManifest" {
		return false
	}
	azdextPackage, ok := verificationFunction.X.(*ast.Ident)
	return ok && azdextPackage.Name == "azdext"
}

func TestHasCanonicalProviderVerificationTestRequiresAssertedError(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "asserted error",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, "extension.yaml"))
}`,
			want: true,
		},
		{
			name: "asserted error followed by another call",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, "extension.yaml"))
	require.True(t, true)
}`,
			want: true,
		},
		{
			name: "discarded error",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	_ = azdext.VerifyProvidersMatchManifest(configureExtensionHost, "extension.yaml")
}`,
		},
		{
			name: "comment only",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	// require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, "extension.yaml"))
}`,
		},
		{
			name: "different test",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {}
func TestOther(t *testing.T) {
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, "extension.yaml"))
}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testPath := filepath.Join(t.TempDir(), "providers_manifest_test.go")
			require.NoError(t, os.WriteFile(testPath, []byte(test.source), 0o600))
			require.Equal(t, test.want, hasCanonicalProviderVerificationTest(t, testPath))
		})
	}
}

// TestCapabilitySchemaTypesInSyncWithGo keeps both JSON schema capability enums
// aligned with the capabilities accepted by azd.
func TestCapabilitySchemaTypesInSyncWithGo(t *testing.T) {
	expected := capabilityStrings()

	t.Run("extension.schema.json", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "extension.schema.json"))
		require.NoError(t, err)

		var schema struct {
			Properties struct {
				Capabilities struct {
					Items struct {
						OneOf []struct {
							Const string `json:"const"`
						} `json:"oneOf"`
					} `json:"items"`
				} `json:"capabilities"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(data, &schema))

		actual := make([]string, 0, len(schema.Properties.Capabilities.Items.OneOf))
		for _, capability := range schema.Properties.Capabilities.Items.OneOf {
			actual = append(actual, capability.Const)
		}
		require.ElementsMatch(t, expected, actual)
	})

	t.Run("registry.schema.json", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "registry.schema.json"))
		require.NoError(t, err)

		var schema struct {
			Definitions struct {
				Version struct {
					Properties struct {
						Capabilities struct {
							Items struct {
								Enum []string `json:"enum"`
							} `json:"items"`
						} `json:"capabilities"`
					} `json:"properties"`
				} `json:"Version"`
			} `json:"definitions"`
		}
		require.NoError(t, json.Unmarshal(data, &schema))

		require.ElementsMatch(t, expected, schema.Definitions.Version.Properties.Capabilities.Items.Enum)
	})
}
