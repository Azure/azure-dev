// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"azurespringboot/internal/deploy"
	"azurespringboot/internal/springmodel"
	"azurespringboot/internal/testutil"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type fakeEnvironmentStore struct {
	name   string
	data   map[string]string
	writes []string
	err    error
	calls  int
}

type fakeDeveloperIdentityResolver struct {
	identity *deploy.DeveloperIdentity
	err      error
	calls    int
}

type fakeDeploymentOutputVerifier struct {
	err       error
	calls     int
	readCalls int
	bound     *deploy.DeploymentBinding
}

func (v *fakeDeploymentOutputVerifier) Read(context.Context, map[string]string) (*deploy.DeploymentBinding, error) {
	v.readCalls++
	return v.bound, v.err
}

func (v *fakeDeploymentOutputVerifier) Verify(
	context.Context,
	springmodel.ResolvedDatabase,
	map[string]string,
) (*deploy.DeploymentBinding, error) {
	v.calls++
	return nil, v.err
}

func (r *fakeDeveloperIdentityResolver) Resolve(
	context.Context,
	string,
) (*deploy.DeveloperIdentity, error) {
	r.calls++
	return r.identity, r.err
}

func (s *fakeEnvironmentStore) Values(context.Context) (string, map[string]string, error) {
	s.calls++
	if s.err != nil {
		return "", nil, s.err
	}
	values := make(map[string]string, len(s.data))
	maps.Copy(values, s.data)
	return s.name, values, nil
}

func (s *fakeEnvironmentStore) Set(_ context.Context, _ string, key, value string) error {
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

func TestProjectLifecycleSkipsProjectsWithoutSpringServices(t *testing.T) {
	store := &fakeEnvironmentStore{err: errors.New("environment must not be read")}
	outputs := &fakeDeploymentOutputVerifier{err: errors.New("deployment must not be read")}
	networkCalls := 0
	runtime := &Runtime{
		environments: store,
		outputs:      outputs,
		prepareNetwork: func(context.Context, *deploy.DeploymentBinding) (func(context.Context) error, error) {
			networkCalls++
			return nil, errors.New("network must not be prepared")
		},
	}
	args := &azdext.ProjectEventArgs{Project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"worker": {Name: "worker", Language: "java", Host: "containerapp"},
		},
	}}

	require.NoError(t, runtime.PreProvision(t.Context(), args))
	require.NoError(t, runtime.PostProvision(t.Context(), args))
	require.Zero(t, store.calls)
	require.Zero(t, outputs.readCalls)
	require.Zero(t, outputs.calls)
	require.Zero(t, networkCalls)
}

func TestPreProvisionPreservesSelectedAdministratorWhenUserChanges(t *testing.T) {
	for _, key := range []string{
		springmodel.DatabasePasswordEnv, springmodel.DatabaseAppPasswordEnv, springmodel.DatabaseAdministratorNameEnv,
		springmodel.DatabaseAdministratorObjectIDEnv, springmodel.DatabaseUserAccessTenantIDEnv,
		"DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	const selectedID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const nextDeployerID = "11111111-2222-3333-4444-555555555555"
	const nextAccessTenantID = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	for _, test := range []struct {
		name       string
		configured string
	}{
		{name: "default-object-id"},
		{name: "explicit-separate-user", configured: selectedID},
		{name: "case-insensitive-aliases", configured: strings.ToUpper(selectedID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := testutil.AutomaticPostgresProject(t)
			values := map[string]string{
				springmodel.AzureSubscriptionIDEnv: "subscription", "DATABASE_ADMIN_NAME": "selected@example.test",
			}
			expectedID := testutil.TestSpringClientID
			if test.configured != "" {
				expectedID = test.configured
				values["DATABASE_ADMIN_OBJECT_ID"] = test.configured
				values[springmodel.DatabaseAdministratorObjectIDEnv] = strings.ToLower(test.configured)
			}
			store := &fakeEnvironmentStore{name: "dev", data: values}
			identities := &fakeDeveloperIdentityResolver{identity: &deploy.DeveloperIdentity{
				ObjectID: testutil.TestSpringClientID, UserAccessTenantID: nextDeployerID,
			}}
			runtime := &Runtime{environments: store, identities: identities, noPrompt: true}
			args := &azdext.ProjectEventArgs{Project: project}
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 1, identities.calls)
			require.True(t, strings.EqualFold(expectedID, store.data["DATABASE_ADMIN_OBJECT_ID"]))
			require.True(t, strings.EqualFold(expectedID, store.data[springmodel.DatabaseAdministratorObjectIDEnv]))
			require.Equal(t, "selected@example.test", store.data[springmodel.DatabaseAdministratorNameEnv])
			require.Equal(t, nextDeployerID, store.data[springmodel.DatabaseUserAccessTenantIDEnv])

			identities.identity.ObjectID = nextDeployerID
			identities.identity.UserAccessTenantID = nextAccessTenantID
			store.writes = nil
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 2, identities.calls, "prepare once for each operation")
			require.True(t, strings.EqualFold(expectedID, store.data[springmodel.DatabaseAdministratorObjectIDEnv]))
			require.Equal(t, []string{springmodel.DatabaseUserAccessTenantIDEnv}, store.writes)
			require.Equal(t, nextAccessTenantID, store.data[springmodel.DatabaseUserAccessTenantIDEnv])
			store.writes = nil
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 3, identities.calls)
			require.Empty(t, store.writes, "repeated preparation must be idempotent")
		})
	}
}

func TestPreProvisionPromptsForMissingAdministratorInputsBeforePersisting(t *testing.T) {
	for _, key := range []string{
		springmodel.DatabasePasswordEnv, springmodel.DatabaseAppPasswordEnv, springmodel.DatabaseAdministratorNameEnv,
		springmodel.DatabaseAdministratorObjectIDEnv, springmodel.DatabaseUserAccessTenantIDEnv,
		"DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	const selectedID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const selectedName = "selected@example.test"
	for _, test := range []struct {
		name          string
		values        map[string]string
		responses     []string
		cancelAt      int
		noPrompt      bool
		expectedID    string
		expectedCalls int
	}{
		{name: "current User default", responses: []string{testutil.TestSpringClientID, selectedName},
			expectedID: testutil.TestSpringClientID, expectedCalls: 2},
		{name: "another User", responses: []string{selectedID, selectedName}, expectedID: selectedID, expectedCalls: 2},
		{name: "configured ID", values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": selectedID},
			responses: []string{selectedName}, expectedID: selectedID, expectedCalls: 1},
		{name: "configured name", values: map[string]string{"DATABASE_ADMIN_NAME": selectedName},
			responses: []string{selectedID}, expectedID: selectedID, expectedCalls: 1},
		{name: "configured pair", values: map[string]string{
			springmodel.DatabaseAdministratorObjectIDEnv: selectedID, springmodel.DatabaseAdministratorNameEnv: selectedName,
		}, expectedID: selectedID},
		{name: "invalid input can be corrected",
			responses:  []string{"", "not-an-id", selectedID, " ", "azd-preprovision-pending", selectedName},
			expectedID: selectedID, expectedCalls: 6},
		{name: "cancel object ID", cancelAt: 1, expectedCalls: 1},
		{name: "cancel name after selecting ID", responses: []string{selectedID}, cancelAt: 2, expectedCalls: 2},
		{name: "no-prompt current User default", noPrompt: true,
			values: map[string]string{"DATABASE_ADMIN_NAME": selectedName}, expectedID: testutil.TestSpringClientID},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := testutil.AutomaticPostgresProject(t)
			values := map[string]string{springmodel.AzureSubscriptionIDEnv: "subscription"}
			maps.Copy(values, test.values)
			store := &fakeEnvironmentStore{name: "dev", data: values}
			identities := &fakeDeveloperIdentityResolver{identity: &deploy.DeveloperIdentity{
				ObjectID: testutil.TestSpringClientID, UserAccessTenantID: testutil.TestSpringClientID,
			}}
			calls := 0
			runtime := &Runtime{
				environments: store, identities: identities, noPrompt: test.noPrompt,
				prompt: func(_ context.Context, options *azdext.PromptOptions) (string, error) {
					calls++
					require.False(t, test.noPrompt)
					require.Empty(t, store.writes, "collect and validate all inputs before persisting")
					require.True(t, options.Required)
					if calls == 1 && values["DATABASE_ADMIN_OBJECT_ID"] == "" &&
						values[springmodel.DatabaseAdministratorObjectIDEnv] == "" {
						require.Equal(t, testutil.TestSpringClientID, options.DefaultValue)
					}
					if calls == test.cancelAt {
						return "", context.Canceled
					}
					require.LessOrEqual(t, calls, len(test.responses))
					return test.responses[calls-1], nil
				},
			}
			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.Equal(t, test.expectedCalls, calls)
			require.Equal(t, 1, identities.calls)
			if test.cancelAt > 0 {
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, store.writes)
				require.NotContains(t, store.data, "DATABASE_ADMIN_NAME")
				return
			}
			require.NoError(t, err)
			require.Equal(t, selectedName, store.data["DATABASE_ADMIN_NAME"])
			require.Equal(t, selectedName, store.data[springmodel.DatabaseAdministratorNameEnv])
			require.Equal(t, test.expectedID, store.data["DATABASE_ADMIN_OBJECT_ID"])
			require.Equal(t, test.expectedID, store.data[springmodel.DatabaseAdministratorObjectIDEnv])
		})
	}
}

func databaseFreeProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	project, service := testutil.CreateMavenProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "pom.xml"), []byte(`<project><dependencies>
	<dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency>
	</dependencies></project>`), 0o600))
	return project, service
}

func databaseFreeApplication() *armappcontainers.ContainerApp {
	return &armappcontainers.ContainerApp{
		ID: new("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/web"),
		Properties: &armappcontainers.ContainerAppProperties{
			Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{}}},
		},
	}
}

func TestDatabaseFreeLifecycleDoesNotAcquirePostgresRequirements(t *testing.T) {
	project, service := databaseFreeProject(t)
	t.Setenv("DATABASE_ADMIN_NAME", "")
	t.Setenv(springmodel.DatabasePasswordEnv, "irrelevant-to-this-application")
	store := &fakeEnvironmentStore{name: "dev", data: map[string]string{}}
	identities := &fakeDeveloperIdentityResolver{err: errors.New("PostgreSQL identity must not be resolved")}
	binding := &deploy.DeploymentBinding{
		App: databaseFreeApplication(), Automatic: true, Values: map[string]string{
			springmodel.PostgresServerIDEnv: "unrelated-postgres-output",
		},
	}
	outputs := &fakeDeploymentOutputVerifier{bound: binding}
	runtime := &Runtime{environments: store, identities: identities, outputs: outputs}
	args := &azdext.ServiceEventArgs{Project: project, Service: service}
	require.NoError(t, runtime.ValidateService(t.Context(), service, project))
	require.NoError(t, runtime.PreBuild(t.Context(), args))
	require.NoError(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.NoError(t, runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.NoError(t, runtime.PreDeploy(t.Context(), args))
	require.Zero(t, identities.calls)
	require.Zero(t, outputs.calls)
	require.Empty(t, store.writes)

	// Mixed-host project with non-Spring service owning administrator bindings must not block DB-free Spring services.
	project.Services["other-api"] = &azdext.ServiceConfig{Name: "other-api", Language: "python", Host: "containerapp"}
	require.NoError(t, os.MkdirAll(filepath.Join(project.Path, "infra"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"), []byte(`{
		"parameters": {
			"databaseAdministratorName": {"value": "${DATABASE_ADMIN_NAME=azd-preprovision-pending}"},
			"databaseAdministratorObjectId": {"value": "${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"}
		}
	}`), 0o600))
	require.NoError(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.Zero(t, identities.calls)
	require.Empty(t, store.writes)
}

func TestMixedProjectKeepsDatabaseFreeServiceIndependent(t *testing.T) {
	for _, freeName := range []string{"a-web", "z-web"} {
		t.Run(freeName, func(t *testing.T) {
			project, service := databaseFreeProject(t)
			service.Name = freeName
			databaseService := &azdext.ServiceConfig{
				Name: "database", Host: springmodel.SpringHost, RelativePath: "database",
			}
			project.Services = map[string]*azdext.ServiceConfig{freeName: service, "database": databaseService}
			root := filepath.Join(project.Path, "database")
			require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "main", "resources"), 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o700))
			for _, name := range []string{"mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")} {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><dependencies>
	<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>
	<dependency><groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId></dependency>
	</dependencies></project>`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main", "resources", "application.properties"),
				[]byte("spring.datasource.url=jdbc:postgresql://pg.example/petclinic?sslmode=verify-full\n"+
					"spring.datasource.username=app\n"+
					"spring.datasource.hikari.data-source-properties[authenticationPluginClassName]="+
					springmodel.AzurePostgreSQLPlugin+"\n"+
					"spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true\n"), 0o600))
			const server = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
			const dbAppID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/database"
			const dbPrincipalID = "11111111-1111-1111-1111-111111111111"
			dbApp := &armappcontainers.ContainerApp{
				ID: new(dbAppID),
				Identity: &armappcontainers.ManagedServiceIdentity{
					Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
					PrincipalID: new(dbPrincipalID),
				},
				Properties: &armappcontainers.ContainerAppProperties{
					Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{}}},
				},
			}
			freeApp := databaseFreeApplication()
			values := map[string]string{
				springmodel.PostgresServerIDEnv:        server,
				springmodel.PostgresHostEnv:            "pg.example",
				springmodel.ProvisionedDatabaseTypeEnv: "postgresql",
			}
			binding := &deploy.DeploymentBinding{
				Automatic: true, Values: values,
				Resources: map[string]bool{
					strings.ToLower(server): true, strings.ToLower(server + "/databases/petclinic"): true,
					strings.ToLower(dbAppID): true, strings.ToLower(*freeApp.ID): true,
				},
				Apps: map[string]*armappcontainers.ContainerApp{
					freeName: freeApp, "database": dbApp,
				},
			}
			store := &fakeEnvironmentStore{name: "dev", data: values}
			runtime := &Runtime{environments: store, outputs: &fakeDeploymentOutputVerifier{bound: binding}}
			ctx := context.WithValue(t.Context(), deploy.DeploymentBindingKey{}, binding)
			services, selected, err := runtime.resolveProject(ctx, project, values, false, true)
			require.NoError(t, err)
			require.Len(t, services, 2)
			require.Equal(t, springmodel.DatabasePostgreSQL, selected.Type)
			require.Equal(t, server, selected.ServerResourceID)
			require.NoError(t, runtime.PreDeploy(t.Context(), &azdext.ServiceEventArgs{Project: project, Service: service}))
			require.Empty(t, store.writes, "a database-free service must not inherit another service's access policy")
		})
	}
}
func TestPreProvisionRejectsInvalidInputsOrUnavailableUserWithoutWrites(t *testing.T) {
	for _, key := range []string{
		springmodel.DatabasePasswordEnv, springmodel.DatabaseAppPasswordEnv, springmodel.DatabaseAdministratorNameEnv,
		springmodel.DatabaseAdministratorObjectIDEnv, springmodel.DatabaseUserAccessTenantIDEnv,
		"DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	for _, test := range []struct {
		name          string
		values        map[string]string
		process       map[string]string
		identityError error
	}{
		{name: "missing-name"},
		{name: "empty-name", values: map[string]string{"DATABASE_ADMIN_NAME": ""}},
		{name: "sentinel-name", values: map[string]string{"DATABASE_ADMIN_NAME": "azd-preprovision-pending"}},
		{name: "empty-id", values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": ""}},
		{name: "zero-id", values: map[string]string{
			"DATABASE_ADMIN_OBJECT_ID": "00000000-0000-0000-0000-000000000000",
		}},
		{name: "invalid-id", values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": "private-invalid-value"}},
		{
			name:   "name-alias-conflict",
			values: map[string]string{springmodel.DatabaseAdministratorNameEnv: "other@example.test"},
		},
		{name: "id-alias-conflict", values: map[string]string{
			"DATABASE_ADMIN_OBJECT_ID":                   testutil.TestSpringClientID,
			springmodel.DatabaseAdministratorObjectIDEnv: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		}},
		{name: "process-conflict", process: map[string]string{"DATABASE_ADMIN_NAME": "other@example.test"}},
		{name: "invalid-process-tenant", process: map[string]string{
			springmodel.DatabaseUserAccessTenantIDEnv: "private-invalid-value",
		}},
		{
			name:          "explicit-id-still-requires-login",
			values:        map[string]string{"DATABASE_ADMIN_OBJECT_ID": testutil.TestSpringClientID},
			identityError: errors.New("host User login unavailable"),
		},
		{
			name:   "explicit-id-does-not-allow-unsupported-deployer",
			values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": testutil.TestSpringClientID},
			identityError: validationError(
				"managed_identity_requires_developer_user", "current principal is not a User", "run azd auth login",
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := testutil.AutomaticPostgresProject(t)
			values := map[string]string{springmodel.AzureSubscriptionIDEnv: "subscription"}
			if test.name != "missing-name" {
				values["DATABASE_ADMIN_NAME"] = "selected@example.test"
			}
			maps.Copy(values, test.values)
			for key, value := range test.process {
				t.Setenv(key, value)
			}
			t.Setenv("AZD_NO_PROMPT", "true")
			store := &fakeEnvironmentStore{name: "dev", data: values}
			identities := &fakeDeveloperIdentityResolver{err: errors.New("identity must not be resolved")}
			if test.identityError != nil {
				identities.err = test.identityError
			}
			runtime := &Runtime{environments: store, identities: identities, noPrompt: true}
			err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-invalid-value")
			require.NotContains(t, err.Error(), "selected@example.test")
			require.NotContains(t, err.Error(), "other@example.test")
			if test.identityError != nil {
				require.ErrorIs(t, err, test.identityError)
				require.Equal(t, 1, identities.calls)
			} else {
				require.Zero(t, identities.calls)
			}
			require.Empty(t, store.writes)
		})
	}
}

func TestAutomaticPostProvisionCleanupRunsBeforeCompletionAndSurvivesHandlerFailureOrCancellation(t *testing.T) {
	scenarios := []string{"success", "repeated-success", "inference-failure", "unresolved-final",
		"cleanup-failure", "cancellation"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			project, service := testutil.AutomaticPostgresProject(t)
			root := "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg/providers/"
			serverID := root + "Microsoft.DBforPostgreSQL/flexibleServers/pg"
			appID := root + "Microsoft.App/containerApps/app"
			identityID := root + "Microsoft.ManagedIdentity/userAssignedIdentities/app"
			bootstrapID := serverID + "/firewallRules/bootstrap-local-ip"
			runtimeID := serverID + "/firewallRules/aca-1-2-3-4"
			binding := &deploy.DeploymentBinding{
				Automatic: true,
				Values: map[string]string{
					springmodel.PostgresServerIDEnv: serverID, springmodel.ContainerAppIDEnv: appID,
					"DATABASE_OUTBOUND_IPS":                      `["1.2.3.4"]`,
					springmodel.PostgresHostEnv:                  "actual.example",
					springmodel.ServiceIdentityIDEnv:             identityID,
					springmodel.ServiceIdentityClientIDEnv:       testutil.TestSpringClientID,
					springmodel.DatabaseAdministratorNameEnv:     "developer@example.test",
					springmodel.DatabaseAdministratorObjectIDEnv: testutil.TestSpringClientID,
					springmodel.DatabaseUserAccessTenantIDEnv:    testutil.TestSpringClientID,
				},
				Outputs: deploy.CurrentDeploymentOutputs{
					"DATABASE_OUTBOUND_IPS": {Type: "string", Value: `["1.2.3.4"]`},
				},
				Resources: map[string]bool{
					strings.ToLower(serverID): true, strings.ToLower(appID): true,
					strings.ToLower(bootstrapID): true, strings.ToLower(runtimeID): true,
					strings.ToLower(identityID):                        true,
					strings.ToLower(serverID + "/databases/petclinic"): true,
				},
				App: &armappcontainers.ContainerApp{
					ID: new(appID),
					Identity: &armappcontainers.ManagedServiceIdentity{
						UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{
							identityID: {ClientID: new(testutil.TestSpringClientID)},
						},
					},
					Properties: &armappcontainers.ContainerAppProperties{
						OutboundIPAddresses: []*string{new("1.2.3.4")},
					},
				},
			}
			if scenario == "repeated-success" || scenario == "cleanup-failure" {
				binding.Values[springmodel.DatabaseAccessConfiguredEnv] = springmodel.AutomaticMIProvisioningVersion
			}
			url := "jdbc:postgresql://actual.example:5432/petclinic"
			if scenario == "inference-failure" {
				url = "invalid"
			} else if scenario == "unresolved-final" {
				url = "${UNRESOLVED_DATASOURCE}"
			}
			binding.App.Properties.Template = &armappcontainers.Template{Containers: []*armappcontainers.Container{{
				Env: []*armappcontainers.EnvironmentVar{
					{Name: new("POSTGRES_URL"), Value: new(url)},
					{Name: new("AZURE_CLIENT_ID"), Value: new(testutil.TestSpringClientID)},
				},
			}}}
			store := &fakeEnvironmentStore{name: "dev", data: binding.Values}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cleanups, sqlCalls := 0, 0
			cleanupFailure := errors.New("cleanup failed")
			runtime := &Runtime{
				environments: store, outputs: &fakeDeploymentOutputVerifier{bound: binding},
				prepareNetwork: func(context.Context, *deploy.DeploymentBinding) (func(context.Context) error, error) {
					return func(cleanupCtx context.Context) error {
						cleanups++
						require.NoError(t, cleanupCtx.Err())
						_, bounded := cleanupCtx.Deadline()
						require.True(t, bounded)
						require.Equal(t, []string{springmodel.DatabaseAccessConfiguredEnv}, store.writes)
						require.Equal(t, "pending", store.data[springmodel.DatabaseAccessConfiguredEnv])
						if scenario == "cleanup-failure" {
							return cleanupFailure
						}
						return nil
					}, nil
				},
				configureAccess: func(context.Context, string, springmodel.ResolvedDatabase, map[string]string) error {
					sqlCalls++
					if scenario == "cancellation" {
						cancel()
						return ctx.Err()
					}
					return nil
				},
			}
			err := runtime.PostProvision(ctx, &azdext.ProjectEventArgs{Project: project})
			if scenario == "inference-failure" || scenario == "unresolved-final" {
				require.Zero(t, cleanups, "invalid datasource selection must not begin network preparation")
			} else {
				require.Equal(t, 1, cleanups)
			}
			if scenario == "success" || scenario == "repeated-success" {
				require.NoError(t, err)
				require.Equal(t, 1, sqlCalls)
				require.Equal(
					t, springmodel.AutomaticMIProvisioningVersion, store.data[springmodel.DatabaseAccessConfiguredEnv],
				)
				store.writes = nil
				runtime.prepareNetwork = func(
					context.Context, *deploy.DeploymentBinding,
				) (func(context.Context) error, error) {
					return nil, nil
				}
				runtime.configureAccess = func(
					context.Context, string, springmodel.ResolvedDatabase, map[string]string,
				) error {
					t.Fatal("automatic managed-identity deploy must not run administrator SQL")
					return nil
				}
				runtime.recoverAccess = func(
					context.Context, string, springmodel.ResolvedDatabase, map[string]string,
				) error {
					t.Fatal("deploy must not recover or mutate temporary administrator registrations")
					return nil
				}
				deployArgs := &azdext.ServiceEventArgs{Project: project, Service: service}
				require.NoError(t, runtime.PreDeploy(t.Context(), deployArgs))
				require.Empty(t, store.writes)
				for _, incomplete := range []string{"pending", "", springmodel.DatabaseAccessPolicyVersion} {
					store.data[springmodel.DatabaseAccessConfiguredEnv] = incomplete
					require.ErrorContains(t, runtime.PreDeploy(t.Context(), deployArgs), "access is incomplete")
				}
			} else {
				require.Error(t, err)
				if scenario == "inference-failure" || scenario == "unresolved-final" {
					require.Empty(t, store.writes)
					require.Zero(t, sqlCalls)
				} else {
					require.Equal(t, []string{springmodel.DatabaseAccessConfiguredEnv}, store.writes)
					require.Equal(t, "pending", store.data[springmodel.DatabaseAccessConfiguredEnv])
				}
				if scenario == "cleanup-failure" {
					require.ErrorIs(t, err, cleanupFailure)
				}
			}
		})
	}
}
