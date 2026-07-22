// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
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
	ID        string     `yaml:"id"`
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
		require.NotEmptyf(t, manifest.ID, "manifest %s is missing the required 'id' field", path)
		manifest.path = path
		manifests = append(manifests, manifest)
	}

	return manifests
}

// TestExtensionManifestsHaveProviderVerificationTests ensures every first-party
// provider extension carries the canonical runtime registration verification test.
func TestExtensionManifestsHaveProviderVerificationTests(t *testing.T) {
	for _, manifest := range loadFirstPartyManifests(t) {
		t.Run(manifest.ID, func(t *testing.T) {
			if len(manifest.Providers) == 0 {
				return
			}

			testPath := filepath.Join(
				filepath.Dir(manifest.path),
				"internal",
				"cmd",
				"providers_manifest_test.go",
			)
			require.Truef(t, hasCanonicalProviderVerificationTest(t, testPath),
				"extension %s declares providers in %s but %s does not define %s asserting "+
					"azdext.VerifyProvidersMatchManifest with require.NoError",
				manifest.ID, manifest.path, testPath, providerVerificationTestName)
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
			if found {
				return false
			}

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
	if !ok || verificationFunction.Sel.Name != "VerifyProvidersMatchManifest" ||
		len(verificationCall.Args) < 1 {
		return false
	}
	azdextPackage, ok := verificationFunction.X.(*ast.Ident)
	if !ok || azdextPackage.Name != "azdext" {
		return false
	}

	configureCallback, ok := verificationCall.Args[0].(*ast.Ident)
	return ok && configureCallback.Name == "configureExtensionHost"
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
			name: "different configure callback",
			source: `package cmd
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureTestHost, "extension.yaml"))
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
