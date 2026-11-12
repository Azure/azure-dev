// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
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
	identity *developerIdentity
	err      error
	calls    int
}

type fakeDeploymentOutputVerifier struct {
	err       error
	calls     int
	readCalls int
	bound     *deploymentBinding
}

func (v *fakeDeploymentOutputVerifier) Read(context.Context, map[string]string) (*deploymentBinding, error) {
	v.readCalls++
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
) (*developerIdentity, error) {
	r.calls++
	return r.identity, r.err
}

func (s *fakeEnvironmentStore) values(context.Context) (string, map[string]string, error) {
	s.calls++
	if s.err != nil {
		return "", nil, s.err
	}
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

func TestProjectLifecycleSkipsProjectsWithoutSpringServices(t *testing.T) {
	store := &fakeEnvironmentStore{err: errors.New("environment must not be read")}
	outputs := &fakeDeploymentOutputVerifier{err: errors.New("deployment must not be read")}
	networkCalls := 0
	runtime := &Runtime{
		environments: store,
		outputs:      outputs,
		prepareNetwork: func(context.Context, *deploymentBinding) (func(context.Context) error, error) {
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
		databasePasswordEnv, databaseAppPasswordEnv, databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv, databaseUserAccessTenantIDEnv, "DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
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
			project, _ := automaticPostgresProject(t)
			values := map[string]string{
				azureSubscriptionIDEnv: "subscription", "DATABASE_ADMIN_NAME": "selected@example.test",
			}
			expectedID := testSpringClientID
			if test.configured != "" {
				expectedID = test.configured
				values["DATABASE_ADMIN_OBJECT_ID"] = test.configured
				values[databaseAdministratorObjectIDEnv] = strings.ToLower(test.configured)
			}
			store := &fakeEnvironmentStore{name: "dev", data: values}
			identities := &fakeDeveloperIdentityResolver{identity: &developerIdentity{
				ObjectID: testSpringClientID, UserAccessTenantID: nextDeployerID,
			}}
			runtime := &Runtime{environments: store, identities: identities, noPrompt: true}
			args := &azdext.ProjectEventArgs{Project: project}
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 1, identities.calls)
			require.True(t, strings.EqualFold(expectedID, store.data["DATABASE_ADMIN_OBJECT_ID"]))
			require.True(t, strings.EqualFold(expectedID, store.data[databaseAdministratorObjectIDEnv]))
			require.Equal(t, "selected@example.test", store.data[databaseAdministratorNameEnv])
			require.Equal(t, nextDeployerID, store.data[databaseUserAccessTenantIDEnv])

			identities.identity.ObjectID = nextDeployerID
			identities.identity.UserAccessTenantID = nextAccessTenantID
			store.writes = nil
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 2, identities.calls, "prepare once for each operation")
			require.True(t, strings.EqualFold(expectedID, store.data[databaseAdministratorObjectIDEnv]))
			require.Equal(t, []string{databaseUserAccessTenantIDEnv}, store.writes)
			require.Equal(t, nextAccessTenantID, store.data[databaseUserAccessTenantIDEnv])
			store.writes = nil
			require.NoError(t, runtime.PreProvision(t.Context(), args))
			require.Equal(t, 3, identities.calls)
			require.Empty(t, store.writes, "repeated preparation must be idempotent")
		})
	}
}

func TestPreProvisionPromptsForMissingAdministratorInputsBeforePersisting(t *testing.T) {
	for _, key := range []string{
		databasePasswordEnv, databaseAppPasswordEnv, databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv, databaseUserAccessTenantIDEnv, "DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
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
		{name: "current User default", responses: []string{testSpringClientID, selectedName},
			expectedID: testSpringClientID, expectedCalls: 2},
		{name: "another User", responses: []string{selectedID, selectedName}, expectedID: selectedID, expectedCalls: 2},
		{name: "configured ID", values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": selectedID},
			responses: []string{selectedName}, expectedID: selectedID, expectedCalls: 1},
		{name: "configured name", values: map[string]string{"DATABASE_ADMIN_NAME": selectedName},
			responses: []string{selectedID}, expectedID: selectedID, expectedCalls: 1},
		{name: "configured pair", values: map[string]string{
			databaseAdministratorObjectIDEnv: selectedID, databaseAdministratorNameEnv: selectedName,
		}, expectedID: selectedID},
		{name: "invalid input can be corrected",
			responses:  []string{"", "not-an-id", selectedID, " ", "azd-preprovision-pending", selectedName},
			expectedID: selectedID, expectedCalls: 6},
		{name: "cancel object ID", cancelAt: 1, expectedCalls: 1},
		{name: "cancel name after selecting ID", responses: []string{selectedID}, cancelAt: 2, expectedCalls: 2},
		{name: "no-prompt current User default", noPrompt: true,
			values: map[string]string{"DATABASE_ADMIN_NAME": selectedName}, expectedID: testSpringClientID},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := automaticPostgresProject(t)
			values := map[string]string{azureSubscriptionIDEnv: "subscription"}
			maps.Copy(values, test.values)
			store := &fakeEnvironmentStore{name: "dev", data: values}
			identities := &fakeDeveloperIdentityResolver{identity: &developerIdentity{
				ObjectID: testSpringClientID, UserAccessTenantID: testSpringClientID,
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
						values[databaseAdministratorObjectIDEnv] == "" {
						require.Equal(t, testSpringClientID, options.DefaultValue)
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
			require.Equal(t, selectedName, store.data[databaseAdministratorNameEnv])
			require.Equal(t, test.expectedID, store.data["DATABASE_ADMIN_OBJECT_ID"])
			require.Equal(t, test.expectedID, store.data[databaseAdministratorObjectIDEnv])
		})
	}
}

func databaseFreeProject(t *testing.T, gradle bool) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	project, service := createMavenProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "pom.xml"), []byte(`<project><dependencies>
	<dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency>
	</dependencies></project>`), 0o600))
	if gradle {
		project.Path = createGradleProject(t, "build.gradle.kts")
		report, err := json.Marshal(gradleReport{
			Version: 1, Root: project.Path, Archive: filepath.Join(project.Path, "build", "app.jar"),
			Dependencies: []string{"org.springframework.boot:spring-boot-starter-web"},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(project.Path, "dependencies.json"), report, 0o600))
		wrapper := `#!/bin/sh
	for argument do
	  case "$argument" in
	    -Dazd.spring.report=*) cp "$(dirname "$0")/dependencies.json" "${argument#*=}"; exit $?;;
	  esac
	done
	exit 1
	`
		if goruntime.GOOS == "windows" {
			wrapper = "@echo off\r\npowershell.exe -NoProfile -File \"%~dp0report.ps1\" %*\r\n"
			require.NoError(t, os.WriteFile(filepath.Join(project.Path, "report.ps1"), []byte(`
	param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
	foreach ($argument in $Arguments) {
	    if ($argument.StartsWith('-Dazd.spring.report=')) {
	        [IO.File]::Copy((Join-Path $PSScriptRoot 'dependencies.json'), $argument.Split('=', 2)[1], $true)
	        exit 0
	    }
	}
	exit 1
	`), 0o600))
		}
		// #nosec G306 -- isolated executable build-report fixture, not a real Gradle invocation.
		require.NoError(t, os.WriteFile(filepath.Join(project.Path, gradleWrapperName()), []byte(wrapper), 0o700))
	}
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
	for _, gradle := range []bool{false, true} {
		t.Run(map[bool]string{false: "Maven", true: "Gradle"}[gradle], func(t *testing.T) {
			project, service := databaseFreeProject(t, gradle)
			t.Setenv("DATABASE_ADMIN_NAME", "")
			t.Setenv(postgresBootstrapClientIPEnv, "invalid-postgres-only-value")
			t.Setenv(databasePasswordEnv, "irrelevant-to-this-application")
			store := &fakeEnvironmentStore{name: "dev", data: map[string]string{}}
			identities := &fakeDeveloperIdentityResolver{err: errors.New("PostgreSQL identity must not be resolved")}
			binding := &deploymentBinding{
				app: databaseFreeApplication(), automatic: true, values: map[string]string{
					postgresServerIDEnv: "unrelated-postgres-output",
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
		})
	}
}

func TestMixedProjectKeepsDatabaseFreeServiceIndependent(t *testing.T) {
	for _, freeName := range []string{"a-web", "z-web"} {
		t.Run(freeName, func(t *testing.T) {
			project, service := databaseFreeProject(t, false)
			service.Name = freeName
			databaseService := &azdext.ServiceConfig{Name: "database", Host: SpringHost, RelativePath: "database"}
			project.Services = map[string]*azdext.ServiceConfig{freeName: service, "database": databaseService}
			root := filepath.Join(project.Path, "database")
			require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "main", "resources"), 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o700))
			for _, name := range []string{"mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")} {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><dependencies>
	<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>
	</dependencies></project>`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main", "resources", "application.properties"),
				[]byte("spring.datasource.url=jdbc:postgresql://pg.example/petclinic?sslmode=verify-full\n"+
					"spring.datasource.username=app\nspring.datasource.password=test-password"), 0o600))
			const server = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg"
			values := map[string]string{
				postgresServerIDEnv: server, postgresHostEnv: "pg.example", provisionedDatabaseTypeEnv: "postgresql",
			}
			binding := &deploymentBinding{
				automatic: true, values: values,
				resources: map[string]bool{
					strings.ToLower(server): true, strings.ToLower(server + "/databases/petclinic"): true,
				},
				apps: map[string]*armappcontainers.ContainerApp{
					freeName: databaseFreeApplication(), "database": databaseFreeApplication(),
				},
			}
			store := &fakeEnvironmentStore{name: "dev", data: values}
			runtime := &Runtime{environments: store, outputs: &fakeDeploymentOutputVerifier{bound: binding}}
			ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
			services, selected, err := runtime.resolveProject(ctx, project, values, false, true)
			require.NoError(t, err)
			require.Len(t, services, 2)
			require.Equal(t, databasePostgreSQL, selected.Type)
			require.Equal(t, server, selected.ServerResourceID)
			require.NoError(t, runtime.PreDeploy(t.Context(), &azdext.ServiceEventArgs{Project: project, Service: service}))
			require.Empty(t, store.writes, "a database-free service must not inherit another service's access policy")
		})
	}
}
func TestPreProvisionRejectsInvalidInputsOrUnavailableUserWithoutWrites(t *testing.T) {
	for _, key := range []string{
		databasePasswordEnv, databaseAppPasswordEnv, databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv, databaseUserAccessTenantIDEnv, "DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
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
		{name: "name-alias-conflict", values: map[string]string{databaseAdministratorNameEnv: "other@example.test"}},
		{name: "id-alias-conflict", values: map[string]string{
			"DATABASE_ADMIN_OBJECT_ID":       testSpringClientID,
			databaseAdministratorObjectIDEnv: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		}},
		{name: "process-conflict", process: map[string]string{"DATABASE_ADMIN_NAME": "other@example.test"}},
		{name: "invalid-process-tenant", process: map[string]string{
			databaseUserAccessTenantIDEnv: "private-invalid-value",
		}},
		{
			name:          "explicit-id-still-requires-login",
			values:        map[string]string{"DATABASE_ADMIN_OBJECT_ID": testSpringClientID},
			identityError: errors.New("host User login unavailable"),
		},
		{
			name:   "explicit-id-does-not-allow-unsupported-deployer",
			values: map[string]string{"DATABASE_ADMIN_OBJECT_ID": testSpringClientID},
			identityError: validationError(
				"managed_identity_requires_developer_user", "current principal is not a User", "run azd auth login",
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project, _ := automaticPostgresProject(t)
			values := map[string]string{azureSubscriptionIDEnv: "subscription"}
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
