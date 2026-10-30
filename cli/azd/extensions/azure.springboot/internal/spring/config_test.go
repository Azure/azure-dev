// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
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
	return &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, Config: config}
}

func TestResolveDatabaseType(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		values  map[string]string
		process map[string]string
		want    databaseType
		wantErr string
	}{
		{name: "missing defaults to h2", want: databaseH2},
		{
			name:   "environment expression",
			config: map[string]any{"database": map[string]any{"type": "${SPRING_DATABASE_TYPE:-h2}"}},
			values: map[string]string{databaseTypeEnv: "postgresql"},
			want:   databasePostgreSQL,
		},
		{
			name:    "explicit empty environment",
			config:  map[string]any{"database": map[string]any{"type": "${SPRING_DATABASE_TYPE:-h2}"}},
			values:  map[string]string{databaseTypeEnv: ""},
			wantErr: "explicitly empty",
		},
		{
			name:    "wrong type",
			config:  map[string]any{"database": map[string]any{"type": true}},
			wantErr: "must be a string",
		},
		{
			name:    "unknown nested field",
			config:  map[string]any{"database": map[string]any{"type": "h2", "auth": "password"}},
			wantErr: "unsupported field",
		},
		{
			name:    "unknown root field",
			config:  map[string]any{"database": map[string]any{"type": "h2"}, "server": "x"},
			wantErr: "unsupported field",
		},
		{
			name:    "unknown mode",
			config:  map[string]any{"database": map[string]any{"type": "mysql"}},
			wantErr: "unsupported value",
		},
		{
			name:    "environment conflicts with literal",
			config:  map[string]any{"database": map[string]any{"type": "h2"}},
			values:  map[string]string{databaseTypeEnv: "postgresql"},
			wantErr: "resolves to",
		},
		{
			name:    "process environment conflicts with literal",
			config:  map[string]any{"database": map[string]any{"type": "h2"}},
			process: map[string]string{databaseTypeEnv: "postgresql"},
			wantErr: "resolves to",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost}
			if test.config != nil {
				service = serviceWithConfig(t, test.config)
			}
			mode, err := resolveDatabaseType(service, test.values, func(key string) (string, bool) {
				value, ok := test.process[key]
				return value, ok
			})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, mode)
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
