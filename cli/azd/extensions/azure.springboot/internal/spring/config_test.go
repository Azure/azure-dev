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
	return &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, AdditionalProperties: config}
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
			name:    "auth must be object",
			config:  map[string]any{"database": map[string]any{"type": "h2", "auth": "password"}},
			wantErr: "must be an object",
		},
		{
			name:   "unrelated service field is preserved",
			config: map[string]any{"database": map[string]any{"type": "h2"}, "server": "x"},
			want:   databaseH2,
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
			wantErr: "selects",
		},
		{
			name:    "process environment conflicts with literal",
			config:  map[string]any{"database": map[string]any{"type": "h2"}},
			process: map[string]string{databaseTypeEnv: "postgresql"},
			wantErr: "selects",
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

func TestResolveManagedDatabaseConfig(t *testing.T) {
	managedIdentityConfig := map[string]any{
		"database": map[string]any{
			"type":             "postgresql",
			"serverResourceId": "${AZURE_POSTGRES_SERVER_ID}",
			"databaseName":     "petclinic",
			"auth": map[string]any{
				"type":     "managedidentity",
				"username": "petclinic_app",

				"appIdentity": map[string]any{
					"type":       "userAssigned",
					"resourceId": "${SERVICE_PETCLINIC_IDENTITY_ID}",
				},
			},
		},
	}
	service := serviceWithConfig(t, managedIdentityConfig)

	deferred, err := resolveDatabase(service, map[string]string{}, func(string) (string, bool) {
		return "", false
	}, false)
	require.NoError(t, err)
	require.True(t, deferred.ServerDeferred)
	require.True(t, deferred.AppIdentity.Deferred)
	require.Equal(t, databaseAuthenticationManagedIdentity, deferred.Authentication)
	require.Equal(t, "petclinic_app", deferred.Username)
	require.Contains(t, deferred.accessFingerprint(), databaseAccessPolicyVersion)

	serverID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
	identityID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/app"
	resolved, err := resolveDatabase(service, map[string]string{
		postgresServerIDEnv:  serverID,
		serviceIdentityIDEnv: identityID,
	}, func(string) (string, bool) {
		return "", false
	}, true)
	require.NoError(t, err)
	require.False(t, resolved.ServerDeferred)
	require.False(t, resolved.AppIdentity.Deferred)
	require.Equal(t, serverID, resolved.ServerResourceID)
	require.Equal(t, identityID, resolved.AppIdentity.ResourceID)

	for _, name := range []string{postgresServerIDEnv, serviceIdentityIDEnv} {
		t.Run("explicit empty "+name, func(t *testing.T) {
			_, err := resolveDatabase(service, map[string]string{name: ""}, func(string) (string, bool) {
				return "", false
			}, false)
			require.ErrorContains(t, err, "explicitly empty")
		})
	}
}

func TestResolveDatabaseAuthValidation(t *testing.T) {
	validAuth := func(authentication string) map[string]any {
		return map[string]any{
			"database": map[string]any{
				"type": "postgresql",
				"serverResourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.DBforPostgreSQL/flexibleServers/pg",
				"databaseName": "petclinic",
				"auth": map[string]any{
					"type":     authentication,
					"username": "petclinic_app",
				},
			},
		}
	}
	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{
			name: "legacy authentication shape rejected",
			config: map[string]any{"database": map[string]any{
				"type": "postgresql", "authentication": "managedidentity",
			}},
			wantErr: "unsupported field",
		},
		{
			name: "identityBinding rejected",
			config: map[string]any{"database": map[string]any{
				"type": "postgresql", "identityBinding": map[string]any{},
			}},
			wantErr: "unsupported field",
		},
		{
			name: "bootstrap rejected",
			config: map[string]any{"database": map[string]any{
				"type": "postgresql", "bootstrap": map[string]any{},
			}},
			wantErr: "unsupported field",
		},
		{
			name: "roleName rejected",
			config: map[string]any{"database": map[string]any{
				"type": "postgresql", "roleName": "app",
			}},
			wantErr: "unsupported field",
		},
		{
			name: "clientIPv4 rejected",
			config: map[string]any{"database": map[string]any{
				"type": "postgresql", "clientIPv4": "203.0.113.1",
			}},
			wantErr: "unsupported field",
		},
		{
			name:    "target without auth rejected",
			config:  map[string]any{"database": map[string]any{"type": "postgresql", "databaseName": "petclinic"}},
			wantErr: "without database.auth",
		},
		{
			name:    "managed identity requires app identity",
			config:  validAuth("managedidentity"),
			wantErr: "requires appIdentity",
		},
		{
			name: "password rejects administrator name",
			config: func() map[string]any {
				config := validAuth("password")
				config["database"].(map[string]any)["auth"].(map[string]any)["administratorName"] = "dev@example.com"
				return config
			}(),
			wantErr: "cannot contain appIdentity or administratorName",
		},
		{
			name: "unsupported permission",
			config: func() map[string]any {
				config := validAuth("password")
				config["database"].(map[string]any)["auth"].(map[string]any)["permission"] = "owner"
				return config
			}(),
			wantErr: "unsupported field",
		},
		{
			name: "username exceeds PostgreSQL identifier limit",
			config: func() map[string]any {
				config := validAuth("password")
				config["database"].(map[string]any)["auth"].(map[string]any)["username"] =
					"app_" + "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefgh"
				return config
			}(),
			wantErr: "unsupported characters",
		},
		{
			name: "h2 rejects target",
			config: map[string]any{"database": map[string]any{
				"type": "h2", "databaseName": "petclinic",
			}},
			wantErr: "selects h2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveDatabase(
				serviceWithConfig(t, test.config),
				map[string]string{},
				func(string) (string, bool) { return "", false },
				false,
			)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestDatabaseIdentifiersRemainCaseSensitive(t *testing.T) {
	left := resolvedDatabase{
		Type: databasePostgreSQL,
		ServerResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.DBforPostgreSQL/flexibleServers/pg",
		Name:           "PetClinic",
		Authentication: databaseAuthenticationPassword,
		Username:       "PetClinic_App",
	}
	right := left
	right.Name = "petclinic"
	require.False(t, compatibleDatabaseSelection(left, right))
	require.NotEqual(t, left.accessFingerprint(), right.accessFingerprint())

	right = left
	right.Username = "petclinic_app"
	require.False(t, compatibleDatabaseSelection(left, right))
	require.NotEqual(t, left.accessFingerprint(), right.accessFingerprint())

	service := serviceWithConfig(t, map[string]any{
		"database": map[string]any{
			"type":             "postgresql",
			"serverResourceId": left.ServerResourceID,
			"databaseName":     left.Name,
			"auth": map[string]any{
				"type":     "password",
				"username": left.Username,
			},
		},
	})
	_, err := resolveDatabase(
		service,
		map[string]string{databaseUsernameEnv: "petclinic_app"},
		func(string) (string, bool) { return "", false },
		false,
	)
	require.ErrorContains(t, err, "selects")
}

func TestResolveDatabaseValidatesLiteralResourceIDsButAllowsDeferredExpressions(t *testing.T) {
	base := map[string]any{
		"type":             "postgresql",
		"serverResourceId": "${AZURE_POSTGRES_SERVER_ID}",
		"databaseName":     "petclinic",
		"auth": map[string]any{
			"type":     "managedidentity",
			"username": "petclinic_app",

			"appIdentity": map[string]any{
				"type":       "userAssigned",
				"resourceId": "${SERVICE_PETCLINIC_IDENTITY_ID}",
			},
		},
	}
	_, err := resolveDatabase(
		serviceWithConfig(t, map[string]any{"database": base}),
		nil,
		func(string) (string, bool) { return "", false },
		false,
	)
	require.NoError(t, err)

	base["serverResourceId"] = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/data"
	_, err = resolveDatabase(
		serviceWithConfig(t, map[string]any{"database": base}),
		nil,
		func(string) (string, bool) { return "", false },
		false,
	)
	require.ErrorContains(t, err, "is not a valid")
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
