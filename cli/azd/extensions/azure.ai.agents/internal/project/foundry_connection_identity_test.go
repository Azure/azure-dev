// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/require"
)

func TestResolveFoundryConnectionServiceIdentityByKeyOrResourceName(t *testing.T) {
	t.Parallel()
	service := promptConnectionService(t, "search-service", " search-resource ")
	services := map[string]*azdext.ServiceConfig{"search-service": service}

	for _, reference := range []string{"search-service", "search-resource"} {
		t.Run(reference, func(t *testing.T) {
			t.Parallel()
			identity, err := ResolveFoundryConnectionServiceIdentity(services, reference, "")
			require.NoError(t, err)
			require.Equal(t, FoundryConnectionServiceIdentity{
				Host:         foundryConnectionHost,
				ServiceKey:   "search-service",
				ResourceName: "search-resource",
			}, identity)
		})
	}
}

func TestResolveFoundryConnectionServiceIdentityUsesKeyFallback(t *testing.T) {
	t.Parallel()
	service := promptConnectionService(t, "search-service", " \t ")
	identity, err := ResolveFoundryConnectionServiceIdentity(
		map[string]*azdext.ServiceConfig{"search-service": service},
		"search-service",
		"",
	)
	require.NoError(t, err)
	require.Equal(t, "search-service", identity.ResourceName)
}

func TestResolveFoundryConnectionServiceIdentityPrioritizesExactKey(t *testing.T) {
	t.Parallel()
	services := map[string]*azdext.ServiceConfig{
		"search-service": promptConnectionService(t, "search-service", "production-search"),
		"production-search": promptConnectionService(
			t,
			"production-search",
			"other-resource",
		),
	}

	identity, err := ResolveFoundryConnectionServiceIdentity(
		services,
		"production-search",
		"",
	)
	require.NoError(t, err)
	require.Equal(t, "production-search", identity.ServiceKey)
	require.Equal(t, "other-resource", identity.ResourceName)
}

func TestResolveFoundryConnectionServiceIdentityRejectsWrongHostKey(t *testing.T) {
	t.Parallel()
	services := map[string]*azdext.ServiceConfig{
		"production-search": {Name: "production-search", Host: foundryToolboxHost},
		"search-service":    promptConnectionService(t, "search-service", "production-search"),
	}

	_, err := ResolveFoundryConnectionServiceIdentity(services, "production-search", "")
	require.ErrorContains(t, err, `resolves to service host "azure.ai.toolbox"`)
	require.ErrorContains(t, err, foundryConnectionHost)
	requireLocalErrorCode(t, err, exterrors.CodeFoundryDependencyNotReady)
}

func TestResolveFoundryConnectionServiceIdentityRejectsUnknownAndBlank(t *testing.T) {
	t.Parallel()
	services := map[string]*azdext.ServiceConfig{
		"search-service": promptConnectionService(t, "search-service", "search-resource"),
	}
	_, err := ResolveFoundryConnectionServiceIdentity(services, "missing", "")
	require.ErrorContains(t, err, "does not match an azure.ai.connection service")
	requireLocalErrorCode(t, err, exterrors.CodeFoundryDependencyNotReady)

	_, err = ResolveFoundryConnectionServiceIdentity(services, " \t ", "")
	require.ErrorContains(t, err, "service key or resource name is required")
	requireLocalErrorCode(t, err, exterrors.CodeInvalidServiceConfig)
}

func TestResolveFoundryConnectionServiceIdentityRejectsDuplicateResourceNames(t *testing.T) {
	t.Parallel()
	services := map[string]*azdext.ServiceConfig{
		"z-search": promptConnectionService(t, "z-search", "shared-name"),
		"a-search": promptConnectionService(t, "a-search", " SHARED-NAME "),
	}

	for range 3 {
		_, err := ResolveFoundryConnectionServiceIdentity(services, "shared-name", "")
		require.ErrorContains(t, err, `matches service keys ["a-search" "z-search"]`)
		requireLocalErrorCode(t, err, exterrors.CodeFoundryDependencyNotReady)
	}
}

func TestResolveFoundryConnectionServiceIdentityUsesEffectiveRefOverlay(t *testing.T) {
	t.Parallel()
	for _, file := range []struct {
		name    string
		content string
	}{
		{name: "connection.yaml", content: "name: referenced-name\n"},
		{name: "connection.json", content: `{"name":"referenced-name"}`},
	} {
		t.Run(file.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, file.name), []byte(file.content), 0o600))

			service := &azdext.ServiceConfig{
				Name: "search-service",
				Host: foundryConnectionHost,
				AdditionalProperties: mustStruct(t, map[string]any{
					"$ref": "./" + file.name,
					"name": "overlay-name",
				}),
			}
			services := map[string]*azdext.ServiceConfig{"search-service": service}

			identity, err := ResolveFoundryConnectionServiceIdentity(
				services,
				"overlay-name",
				root,
			)
			require.NoError(t, err)
			require.Equal(t, "search-service", identity.ServiceKey)
			require.Equal(t, "overlay-name", identity.ResourceName)

			_, err = ResolveFoundryConnectionServiceIdentity(
				services,
				"referenced-name",
				root,
			)
			require.Error(t, err, "the overlay name replaces the referenced name")
		})
	}
}

func TestResolveFoundryConnectionServiceIdentityRejectsMalformedRef(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "broken.yaml"),
		[]byte("name: ["),
		0o600,
	))
	service := &azdext.ServiceConfig{
		Name: "broken",
		Host: foundryConnectionHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"$ref": "./broken.yaml",
		}),
	}

	_, err := ResolveFoundryConnectionServiceIdentity(
		map[string]*azdext.ServiceConfig{"broken": service},
		"broken",
		root,
	)
	require.ErrorContains(t, err, "not a valid YAML or JSON object")
	requireLocalErrorCode(t, err, foundry.CodeInvalidFileRef)
}

func requireLocalErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, code, localErr.Code)
}
