// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func serviceWithConfig(t *testing.T, value map[string]any) *azdext.ServiceConfig {
	t.Helper()
	config, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, AdditionalProperties: config}
}

func requireLocalErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, code, local.Code)
}

func TestValidateAutomaticServiceConfigRejectsExplicitSettings(t *testing.T) {
	require.NoError(t, validateAutomaticServiceConfig(&azdext.ServiceConfig{Name: "petclinic", Host: SpringHost}))

	t.Run("database", func(t *testing.T) {
		for _, value := range []any{nil, map[string]any{}, map[string]any{"type": "postgresql"}} {
			service := serviceWithConfig(t, map[string]any{"database": value})
			err := validateAutomaticServiceConfig(service)
			require.Error(t, err)
			requireLocalErrorCode(t, err, "explicit_database_config_unsupported")
		}
	})

	t.Run("legacy database", func(t *testing.T) {
		config, err := structpb.NewStruct(map[string]any{"database": map[string]any{}})
		require.NoError(t, err)
		service := serviceWithConfig(t, nil)
		service.Config = config
		err = validateAutomaticServiceConfig(service)
		require.Error(t, err)
		requireLocalErrorCode(t, err, "explicit_database_config_unsupported")
	})

	t.Run("parser inferred docker options", func(t *testing.T) {
		service := serviceWithConfig(t, nil)
		service.Docker = &azdext.DockerProjectOptions{Path: "Dockerfile", Context: ".", Platform: "linux/amd64"}
		require.NoError(t, validateAutomaticServiceConfig(service))
	})
}

func TestValidateRawServiceSettingsRejectsPresence(t *testing.T) {
	require.NoError(t, validateRawServiceSettings(nil))

	for _, test := range []struct {
		name string
		raw  map[string]any
		code string
	}{
		{"empty database", map[string]any{"database": map[string]any{}}, "explicit_database_config_unsupported"},
		{"null database", map[string]any{"database": nil}, "explicit_database_config_unsupported"},
		{"legacy database", map[string]any{
			"config": map[string]any{"database": map[string]any{}},
		}, "explicit_database_config_unsupported"},
		{"empty docker", map[string]any{"docker": map[string]any{}}, "explicit_docker_config_unsupported"},
		{"null docker", map[string]any{"docker": nil}, "explicit_docker_config_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateRawServiceSettings(test.raw)
			require.Error(t, err)
			requireLocalErrorCode(t, err, test.code)
		})
	}
}

func TestEnvironmentLookupPrefersPersistedValue(t *testing.T) {
	t.Setenv("VALUE", "process")
	value, present := environmentLookup(map[string]string{"VALUE": "persisted"})("VALUE")
	require.True(t, present)
	require.Equal(t, "persisted", value)

	require.NoError(t, os.Unsetenv("VALUE"))
}

func TestSpringServicesSelectCanonicalHostOnly(t *testing.T) {
	canonical := &azdext.ServiceConfig{Name: "canonical", Host: SpringHost}
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"canonical": canonical,
		"legacy":    {Name: "legacy", Host: "azure.springboot"},
		"native":    {Name: "native", Host: "containerapp"},
	}}

	require.Equal(t, []*azdext.ServiceConfig{canonical}, springServices(project))
}
