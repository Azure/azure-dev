// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"maps"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeEnvironmentStore struct {
	name   string
	data   map[string]string
	writes []string
}

type fakeDeploymentSafetyChecker struct {
	err   error
	calls int
}

type fakeDeveloperIdentityResolver struct {
	identity *developerIdentity
	err      error
	calls    int
}

type fakeUserAccessTenantResolver struct {
	tenant string
	err    error
	calls  int
}

func (r *fakeUserAccessTenantResolver) Resolve(context.Context, string) (string, error) {
	r.calls++
	return r.tenant, r.err
}

type fakeDeploymentOutputVerifier struct {
	err   error
	calls int
	bound *deploymentBinding
}

func (v *fakeDeploymentOutputVerifier) Read(context.Context, map[string]string) (*deploymentBinding, error) {
	return v.bound, v.err
}

func (v *fakeDeploymentOutputVerifier) Verify(
	context.Context,
	resolvedDatabase,
	map[string]string,
) (*deploymentBinding, error) {
	v.calls++
	return nil, v.err
}

func (r *fakeDeveloperIdentityResolver) Resolve(
	context.Context,
	string,
	string,
) (*developerIdentity, error) {
	r.calls++
	return r.identity, r.err
}

func (c *fakeDeploymentSafetyChecker) EnsureCanGenerate(
	context.Context,
	string,
	map[string]string,
) error {
	c.calls++
	return c.err
}

func (s *fakeEnvironmentStore) values(context.Context) (string, map[string]string, error) {
	values := make(map[string]string, len(s.data))
	maps.Copy(values, s.data)
	return s.name, values, nil
}

func (s *fakeEnvironmentStore) set(_ context.Context, _ string, key, value string) error {
	s.data[key] = value
	s.writes = append(s.writes, key)
	return nil
}

func clearProcessValue(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestPreProvisionWritesModeAndPasswordForSameInvocation(t *testing.T) {
	clearProcessValue(t, databaseTypeEnv)
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	deployments := &fakeDeploymentSafetyChecker{}
	runtime := &Runtime{
		environments: store,
		deployments:  deployments,
		reconcile: func(context.Context, map[string]string) error {
			return nil
		},
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, "postgresql", store.data[databaseTypeEnv])
	require.NotEmpty(t, store.data[databasePasswordEnv])
	require.ElementsMatch(t, []string{databaseTypeEnv, databasePasswordEnv}, store.writes)
	require.Equal(t, 1, deployments.calls)
}

func TestPreProvisionValidatesFilesBeforeEnvironmentWrites(t *testing.T) {
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).AdditionalProperties
	require.NoError(t, os.RemoveAll(project.Path+string(os.PathSeparator)+"src"))
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	runtime := &Runtime{environments: store}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "missing required Maven project path")
	require.Empty(t, store.writes)
	require.Empty(t, store.data)
}

func TestPreProvisionRejectsInvalidSuppliedPasswordsBeforeWritesOrAzureReads(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).AdditionalProperties

	for _, test := range []struct {
		name      string
		persisted string
		provided  string
		wantErr   string
	}{
		{
			name:     "explicit empty",
			provided: "",
			wantErr:  "explicitly empty",
		},
		{
			name:      "conflict",
			persisted: "Valid-Pass9!",
			provided:  "Other-Pass8!",
			wantErr:   "conflicts",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(databasePasswordEnv, test.provided)
			values := map[string]string{}
			if test.persisted != "" {
				values[databasePasswordEnv] = test.persisted
			}
			store := &fakeEnvironmentStore{name: "test", data: values}
			deployments := &fakeDeploymentSafetyChecker{}
			runtime := &Runtime{
				environments: store,
				deployments:  deployments,
			}

			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.ErrorContains(t, err, test.wantErr)
			require.Empty(t, store.writes)
			require.Zero(t, deployments.calls)
		})
	}
}

func TestPreProvisionFailsClosedBeforeWritesWhenDeploymentSafetyCannotBeEstablished(t *testing.T) {
	clearProcessValue(t, databasePasswordEnv)
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	deployments := &fakeDeploymentSafetyChecker{err: assertError("resource ownership read failed")}
	runtime := &Runtime{
		environments: store,
		deployments:  deployments,
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "resource ownership read failed")
	require.Empty(t, store.writes)
	require.Empty(t, store.data)
	require.Equal(t, 1, deployments.calls)
}

func TestPostProvisionDoesNotPersistCompletionBeforeFirewallSuccess(t *testing.T) {
	clearProcessValue(t, databaseTypeEnv)
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "postgresql"},
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	runtime := &Runtime{
		environments: store,
		reconcile: func(context.Context, map[string]string) error {
			return assertError("firewall denied")
		},
	}

	err := runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "firewall denied")
	require.Empty(t, store.data[provisionedDatabaseTypeEnv])
	require.Empty(t, store.writes)
}

func TestPreProvisionRejectsInPlaceModeSwitch(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{"type": "h2"},
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		provisionedDatabaseTypeEnv: "postgresql",
	}}
	runtime := &Runtime{environments: store}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "provisioned for")
	require.Empty(t, store.writes)
}

type assertError string

func (e assertError) Error() string {
	return string(e)
}

func TestResolveServiceRejectsLegacyHost(t *testing.T) {
	runtime := &Runtime{}

	_, err := runtime.resolveService(t.Context(), nil, &azdext.ServiceConfig{
		Name: "petclinic",
		Host: "azure.springboot",
	}, nil, nil, false)
	require.ErrorContains(t, err, `service "petclinic" uses unsupported Spring host "azure.springboot"`)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, "unsupported_spring_host", localErr.Code)
	require.Equal(t, "set the service host to springbootapp", localErr.Suggestion)
}

func TestPreProvisionManagedIdentityPersistsVerifiedDeveloperBindings(t *testing.T) {
	for _, name := range []string{
		databasePasswordEnv,
		databaseAppPasswordEnv,
		databaseAuthenticationEnv,
		databaseNameEnv,
		databaseUsernameEnv,
		databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv,
		databaseUserAccessTenantIDEnv,
	} {
		clearProcessValue(t, name)
	}
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
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
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		azureSubscriptionIDEnv: "subscription",
	}}
	identities := &fakeDeveloperIdentityResolver{identity: &developerIdentity{
		ObjectID:           "developer-object-id",
		AdministratorName:  "developer@example.com",
		UserAccessTenantID: "user-tenant",
	}}
	runtime := &Runtime{
		environments: store,
		identities:   identities,
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, 1, identities.calls)
	require.Equal(t, "managedidentity", store.data[databaseAuthenticationEnv])
	require.Equal(t, "petclinic", store.data[databaseNameEnv])
	require.Equal(t, "petclinic_app", store.data[databaseUsernameEnv])
	require.Equal(t, "developer@example.com", store.data[databaseAdministratorNameEnv])
	require.Equal(t, "developer-object-id", store.data[databaseAdministratorObjectIDEnv])
	require.Equal(t, "user-tenant", store.data[databaseUserAccessTenantIDEnv])
	require.NotContains(t, store.data, databasePasswordEnv)
	require.NotContains(t, store.data, databaseAppPasswordEnv)
}

func TestPreProvisionManagedIdentityRejectsPasswordBeforeIdentityLookup(t *testing.T) {
	clearProcessValue(t, databaseAppPasswordEnv)
	t.Setenv(databasePasswordEnv, "")
	project, service := createMavenProject(t)
	service.AdditionalProperties = serviceWithConfig(t, map[string]any{
		"database": map[string]any{
			"type": "postgresql",
			"serverResourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.DBforPostgreSQL/flexibleServers/pg",
			"databaseName": "petclinic",
			"auth": map[string]any{
				"type":     "managedidentity",
				"username": "petclinic_app",

				"appIdentity": map[string]any{
					"type": "userAssigned",
					"resourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
						"Microsoft.ManagedIdentity/userAssignedIdentities/app",
				},
			},
		},
	}).AdditionalProperties
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		azureSubscriptionIDEnv: "subscription",
	}}
	identities := &fakeDeveloperIdentityResolver{}
	runtime := &Runtime{environments: store, identities: identities}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "cannot be supplied")
	require.Zero(t, identities.calls)
	require.Empty(t, store.writes)
}

func TestPreProvisionPasswordCreatesSeparateStableCredentials(t *testing.T) {
	for _, name := range []string{databasePasswordEnv, databaseAppPasswordEnv} {
		clearProcessValue(t, name)
	}
	project, service := createMavenProject(t)
	service.AdditionalProperties = passwordManagedAccessConfig(t)
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{}}
	deployments := &fakeDeploymentSafetyChecker{}
	tenants := &fakeUserAccessTenantResolver{tenant: "user-tenant"}
	store.data[azureSubscriptionIDEnv] = "subscription"
	runtime := &Runtime{
		environments: store,
		deployments:  deployments,
		tenants:      tenants,
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.NotEmpty(t, store.data[databasePasswordEnv])
	require.NotEmpty(t, store.data[databaseAppPasswordEnv])
	require.NotEqual(t, store.data[databasePasswordEnv], store.data[databaseAppPasswordEnv])
	require.Equal(t, "password", store.data[databaseAuthenticationEnv])
	require.Equal(t, "user-tenant", store.data[databaseUserAccessTenantIDEnv])
	require.NotContains(t, store.data, "SPRING_DATABASE_INITIALIZATION_COMPLETED")
	require.Equal(t, 1, deployments.calls)
	require.Equal(t, 1, tenants.calls)

	firstAdministrator := store.data[databasePasswordEnv]
	firstApplication := store.data[databaseAppPasswordEnv]
	store.writes = nil
	err = runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, firstAdministrator, store.data[databasePasswordEnv])
	require.Equal(t, firstApplication, store.data[databaseAppPasswordEnv])
	require.Empty(t, store.writes)
	require.Equal(t, 2, tenants.calls)
}

func TestPostProvisionManagedAccessCompletesOnlyAfterSQLAndFirewall(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = passwordManagedAccessConfig(t)
	serverID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		databaseTypeEnv:           "postgresql",
		databaseAuthenticationEnv: "password",
		databaseNameEnv:           "petclinic",
		databaseUsernameEnv:       "petclinic_app",
		databasePasswordEnv:       "Admin-Pass9!",
		databaseAppPasswordEnv:    "App-Pass8!",
		postgresServerIDEnv:       serverID,
		postgresDatabaseEnv:       "petclinic",
		postgresAdministratorEnv:  "petclinicadmin",
	}}
	order := []string{}
	runtime := &Runtime{
		environments: store,
		outputs:      &fakeDeploymentOutputVerifier{},
		configureAccess: func(
			_ context.Context,
			envName string,
			database resolvedDatabase,
			values map[string]string,
		) error {
			order = append(order, "sql")
			require.Equal(t, "test", envName)
			require.Equal(t, databaseAuthenticationPassword, database.Authentication)
			require.Equal(t, "petclinicadmin", database.AdministratorName)
			require.Equal(t, "Admin-Pass9!", values[databasePasswordEnv])
			return nil
		},
		reconcile: func(context.Context, map[string]string) error {
			order = append(order, "firewall")
			return nil
		},
	}

	err := runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, []string{"sql", "firewall"}, order)
	require.Equal(t, databaseAccessPolicyVersion, store.data[databaseAccessConfiguredEnv])
	require.Equal(t, "postgresql", store.data[provisionedDatabaseTypeEnv])
	require.NotEmpty(t, store.data[provisionedDatabaseAccessEnv])
	require.Equal(t, "petclinicadmin", store.data[databaseAdministratorNameEnv])
	require.NotContains(t, store.data, "SPRING_DATABASE_INITIALIZATION_COMPLETED")
	require.NotContains(t, store.data, "SPRING_DATABASE_INITIALIZATION_TARGET")
}

func TestPostProvisionManagedAccessFailureLeavesCompletionUnset(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = passwordManagedAccessConfig(t)
	serverID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		databaseTypeEnv:           "postgresql",
		databaseAuthenticationEnv: "password",
		databaseNameEnv:           "petclinic",
		databaseUsernameEnv:       "petclinic_app",
		databasePasswordEnv:       "Admin-Pass9!",
		databaseAppPasswordEnv:    "App-Pass8!",
		postgresServerIDEnv:       serverID,
		postgresDatabaseEnv:       "petclinic",
		postgresAdministratorEnv:  "petclinicadmin",
	}}
	runtime := &Runtime{
		environments: store,
		outputs:      &fakeDeploymentOutputVerifier{},
		configureAccess: func(
			context.Context,
			string,
			resolvedDatabase,
			map[string]string,
		) error {
			return assertError("SQL setup failed")
		},
		reconcile: func(context.Context, map[string]string) error {
			t.Fatal("firewall must not run after SQL setup failure")
			return nil
		},
	}

	err := runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.ErrorContains(t, err, "SQL setup failed")
	require.Empty(t, store.data[databaseAccessConfiguredEnv])
	require.Empty(t, store.data[provisionedDatabaseAccessEnv])
	require.Empty(t, store.writes)
}

func TestPostProvisionManagedIdentityReloadsVerifiedAdministrator(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = managedIdentityAccessConfig(t)
	serverID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
	identityID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/petclinic"
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		databaseTypeEnv:                  "postgresql",
		databaseAuthenticationEnv:        "managedidentity",
		databaseNameEnv:                  "petclinic",
		databaseUsernameEnv:              "petclinic_app",
		databaseAdministratorNameEnv:     "developer@example.com",
		databaseAdministratorObjectIDEnv: "developer-object-id",
		databaseUserAccessTenantIDEnv:    "user-tenant-id",
		postgresServerIDEnv:              serverID,
		postgresDatabaseEnv:              "petclinic",
		serviceIdentityIDEnv:             identityID,
	}}
	runtime := &Runtime{
		environments: store,
		outputs:      &fakeDeploymentOutputVerifier{},
		configureAccess: func(
			_ context.Context,
			_ string,
			database resolvedDatabase,
			_ map[string]string,
		) error {
			require.Equal(t, databaseAuthenticationManagedIdentity, database.Authentication)
			require.Equal(t, "developer@example.com", database.AdministratorName)
			return nil
		},
		reconcile: func(context.Context, map[string]string) error {
			return nil
		},
	}

	err := runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, databaseAccessPolicyVersion, store.data[databaseAccessConfiguredEnv])
}

func TestPreProvisionPasswordPersistsTenantWithoutDeveloperIdentityLookup(t *testing.T) {
	for _, name := range []string{
		databasePasswordEnv,
		databaseAppPasswordEnv,
		databaseUserAccessTenantIDEnv,
	} {
		clearProcessValue(t, name)
	}
	project, service := createMavenProject(t)
	service.AdditionalProperties = passwordManagedAccessConfig(t)
	store := &fakeEnvironmentStore{name: "test", data: map[string]string{
		azureSubscriptionIDEnv: "subscription",
	}}
	tenants := &fakeUserAccessTenantResolver{tenant: "password-user-tenant"}
	identities := &fakeDeveloperIdentityResolver{}
	runtime := &Runtime{
		environments: store,
		deployments:  &fakeDeploymentSafetyChecker{},
		identities:   identities,
		tenants:      tenants,
	}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
	require.NoError(t, err)
	require.Equal(t, "password-user-tenant", store.data[databaseUserAccessTenantIDEnv])
	require.Equal(t, 1, tenants.calls)
	require.Zero(t, identities.calls)
}

func TestPreProvisionRejectsInvalidLiteralResourceIDsWithoutWritesOrIdentityLookup(t *testing.T) {
	for _, test := range []struct {
		name        string
		serverID    string
		identityID  string
		managedAuth bool
	}{
		{
			name:     "invalid server",
			serverID: "not-an-arm-id",
		},
		{
			name: "wrong identity type",
			serverID: "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.DBforPostgreSQL/flexibleServers/pg",
			identityID:  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/app",
			managedAuth: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, service := createMavenProject(t)
			auth := map[string]any{
				"type":     "password",
				"username": "petclinic_app",
			}
			if test.managedAuth {
				auth["type"] = "managedidentity"
				auth["appIdentity"] = map[string]any{
					"type":       "userAssigned",
					"resourceId": test.identityID,
				}
			}
			service.AdditionalProperties = serviceWithConfig(t, map[string]any{
				"database": map[string]any{
					"type":             "postgresql",
					"serverResourceId": test.serverID,
					"databaseName":     "petclinic",
					"auth":             auth,
				},
			}).AdditionalProperties
			store := &fakeEnvironmentStore{name: "test", data: map[string]string{
				azureSubscriptionIDEnv: "subscription",
			}}
			identities := &fakeDeveloperIdentityResolver{}
			tenants := &fakeUserAccessTenantResolver{tenant: "tenant"}
			runtime := &Runtime{
				environments: store,
				identities:   identities,
				tenants:      tenants,
			}

			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.ErrorContains(t, err, "is not a valid")
			require.Empty(t, store.writes)
			require.Zero(t, identities.calls)
			require.Zero(t, tenants.calls)
		})
	}
}

func TestManagedAccessDeployRequiresCurrentAccessPolicyNotInitializerReceipt(t *testing.T) {
	project, service := createMavenProject(t)
	service.AdditionalProperties = passwordManagedAccessConfig(t)
	database := mustResolveDatabase(t, service, nil)
	for _, test := range []struct {
		name    string
		policy  string
		wantErr bool
	}{
		{name: "missing", wantErr: true},
		{name: "old DML-only policy", policy: "true", wantErr: true},
		{name: "stale policy", policy: "app-owned-v1", wantErr: true},
		{name: "startup initializes schema", policy: databaseAccessPolicyVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeEnvironmentStore{name: "test", data: map[string]string{
				provisionedDatabaseTypeEnv:   "postgresql",
				provisionedDatabaseAccessEnv: database.accessFingerprint(),
				databaseAccessConfiguredEnv:  test.policy,
			}}
			reconciles := 0
			runtime := &Runtime{
				environments: store,
				outputs:      &fakeDeploymentOutputVerifier{},
				configureAccess: func(context.Context, string, resolvedDatabase, map[string]string) error {
					return nil
				},
				reconcile: func(context.Context, map[string]string) error {
					reconciles++
					return nil
				},
			}
			err := runtime.PreDeploy(t.Context(), &azdext.ServiceEventArgs{
				Project: project,
				Service: service,
			})
			if test.wantErr {
				require.ErrorContains(t, err, "application access is incomplete")
				require.Zero(t, reconciles)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, reconciles)
		})
	}
}

func mustResolveDatabase(
	t *testing.T,
	service *azdext.ServiceConfig,
	values map[string]string,
) resolvedDatabase {
	t.Helper()
	database, err := resolveDatabase(service, values, func(string) (string, bool) {
		return "", false
	}, true)
	require.NoError(t, err)
	return database
}

func passwordManagedAccessConfig(t *testing.T) *structpb.Struct {
	t.Helper()
	return serviceWithConfig(t, map[string]any{
		"database": map[string]any{
			"type": "postgresql",
			"serverResourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.DBforPostgreSQL/flexibleServers/pg",
			"databaseName": "petclinic",
			"auth": map[string]any{
				"type":     "password",
				"username": "petclinic_app",
			},
		},
	}).AdditionalProperties
}

func managedIdentityAccessConfig(t *testing.T) *structpb.Struct {
	t.Helper()
	return serviceWithConfig(t, map[string]any{
		"database": map[string]any{
			"type": "postgresql",
			"serverResourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.DBforPostgreSQL/flexibleServers/pg",
			"databaseName": "petclinic",
			"auth": map[string]any{
				"type":     "managedidentity",
				"username": "petclinic_app",

				"appIdentity": map[string]any{
					"type": "userAssigned",
					"resourceId": "/subscriptions/sub/resourceGroups/rg/providers/" +
						"Microsoft.ManagedIdentity/userAssignedIdentities/petclinic",
				},
			},
		},
	}).AdditionalProperties
}
