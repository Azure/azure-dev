// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeManifest(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extension.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestVerifyProvidersMatchManifest_Match(t *testing.T) {
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: custom.host
    type: service-target
    description: d
  - name: custom.provider
    type: provisioning-provider
    description: d
`)

	factoryInvoked := false
	configure := func(host *ExtensionHost) {
		host.
			WithServiceTarget("custom.host", func() ServiceTargetProvider {
				factoryInvoked = true
				return nil
			}).
			WithProvisioningProvider("custom.provider", func() ProvisioningProvider {
				factoryInvoked = true
				return nil
			})
	}

	require.NoError(t, VerifyProvidersMatchManifest(configure, manifest))
	require.False(t, factoryInvoked, "provider factories must remain lazy during verification")
}

func TestVerifyProvidersMatchManifest_DeadClaim(t *testing.T) {
	// Manifest declares a service target the code never registers.
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: custom.host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "declared in")
	require.Contains(t, err.Error(), "not registered")
}

func TestVerifyProvidersMatchManifest_UndeclaredRegistration(t *testing.T) {
	// Code registers a provider the manifest does not declare.
	manifest := writeManifest(t, `
id: publisher.extension
providers: []
`)

	configure := func(host *ExtensionHost) {
		host.WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil })
	}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "registered by the extension but not declared")
}

func TestVerifyProvidersMatchManifest_IgnoresFrameworkAndValidation(t *testing.T) {
	// Framework and validation registrations are not represented in providers: and
	// must not cause a mismatch.
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: custom.host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {
		host.
			WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil }).
			WithFrameworkService("rust", func() FrameworkServiceProvider { return nil })
	}

	require.NoError(t, VerifyProvidersMatchManifest(configure, manifest))
}

func TestVerifyProvidersMatchManifest_DuplicateRegistration(t *testing.T) {
	// A duplicate registration is rejected at runtime, so the check must flag it.
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: custom.host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {
		host.
			WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil }).
			WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil })
	}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "registered more than once")
}

func TestVerifyProvidersMatchManifest_DuplicateManifestEntry(t *testing.T) {
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: custom.host
    type: service-target
    description: d
  - name: custom.host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {
		host.WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil })
	}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "declared more than once")
}

func TestVerifyProvidersMatchManifest_NilConfigure(t *testing.T) {
	manifest := writeManifest(t, "id: publisher.extension\n")
	require.Error(t, VerifyProvidersMatchManifest(nil, manifest))
}

func TestVerifyProvidersMatchManifest_CaseMismatch(t *testing.T) {
	// Runtime provider registration uses exact string keys, so a casing-only
	// difference between the manifest and code must be reported.
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: Custom.Host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {
		host.WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil })
	}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), `provider "Custom.Host"`)
	require.Contains(t, err.Error(), `registered as "custom.host"`)
	require.Contains(t, err.Error(), "provider names are case-sensitive")
	require.NotContains(t, err.Error(), "not registered")
	require.NotContains(t, err.Error(), "not declared")
}

func TestVerifyProvidersMatchManifest_CaseInsensitiveDuplicate(t *testing.T) {
	// Duplicate detection is case-insensitive, matching core.
	manifest := writeManifest(t, `
id: publisher.extension
providers:
  - name: Custom.Host
    type: service-target
    description: d
  - name: custom.host
    type: service-target
    description: d
`)

	configure := func(host *ExtensionHost) {
		host.WithServiceTarget("custom.host", func() ServiceTargetProvider { return nil })
	}

	err := VerifyProvidersMatchManifest(configure, manifest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "declared more than once")
	require.NotContains(t, err.Error(), "not registered")
}
