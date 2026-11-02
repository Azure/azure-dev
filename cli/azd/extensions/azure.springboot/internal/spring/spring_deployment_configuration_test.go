// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func automaticPostgresProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	project, service := createMavenProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "pom.xml"), []byte(`<project><dependencies>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId><scope>runtime</scope></dependency>
<dependency><groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId><scope>runtime</scope></dependency>
</dependencies></project>`), 0o600))
	resourceDir := filepath.Join(project.Path, "src", "main", "resources")
	require.NoError(t, os.MkdirAll(resourceDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(resourceDir, "application.properties"), []byte(
		"spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}\n"+
			"spring.datasource.username=petclinic_app\n"+
			"spring.datasource.hikari.data-source-properties[authenticationPluginClassName]="+azurePostgreSQLPlugin+"\n"+
			"spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true\n"+
			"spring.datasource.hikari.data-source-properties[azure.clientId]=${AZURE_CLIENT_ID:}\n"+
			"spring.datasource.hikari.data-source-properties[sslmode]=verify-full\n"+
			"spring.datasource.hikari.data-source-properties[sslrootcert]=/etc/ssl/certs/ca-certificates.crt\n",
	), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(project.Path, "infra"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"), []byte(`{
"parameters":{
"databaseAdministratorObjectId":{"value":"${DATABASE_ADMIN_OBJECT_ID}"},
"databaseAdministratorName":{"value":"${DATABASE_ADMIN_NAME}"}
}}`), 0o600))
	return project, service
}

func TestAutomaticPreProvisionPreparesOnlyBoundAdministratorInputs(t *testing.T) {
	for _, key := range []string{
		databasePasswordEnv, databaseAppPasswordEnv, databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv, "DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	project, _ := automaticPostgresProject(t)
	store := &fakeEnvironmentStore{name: "dev", data: map[string]string{azureSubscriptionIDEnv: "subscription"}}
	identities := &fakeDeveloperIdentityResolver{identity: &developerIdentity{
		AdministratorName: "developer@example.test", ObjectID: testSpringClientID, UserAccessTenantID: testSpringClientID,
	}}
	runtime := &Runtime{environments: store, identities: identities}
	require.NoError(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.Equal(t, 1, identities.calls)
	require.Equal(t, "developer@example.test", store.data["DATABASE_ADMIN_NAME"])
	require.Equal(t, testSpringClientID, store.data["DATABASE_ADMIN_OBJECT_ID"])
	for _, key := range []string{
		databaseTypeEnv, databaseAuthenticationEnv, databaseNameEnv, databaseUsernameEnv,
		provisionedDatabaseTypeEnv, provisionedDatabaseAccessEnv, databaseAccessConfiguredEnv,
		databasePasswordEnv, databaseAppPasswordEnv,
	} {
		require.NotContains(t, store.data, key)
	}
	store.writes = nil
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "infra", "main.parameters.json"), []byte(
		`{"parameters":{"databaseAdministratorName":{"value":"unbound"}}}`,
	), 0o600))
	require.Error(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.Equal(t, 1, identities.calls)
	require.Empty(t, store.writes)
}

func TestDeployedSpringConfigurationResolvesActualSecretReferencesAndLaunchArguments(t *testing.T) {
	app := &armappcontainers.ContainerApp{Properties: &armappcontainers.ContainerAppProperties{
		Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{
			Env: []*armappcontainers.EnvironmentVar{
				{Name: new("POSTGRES_URL"), Value: new("jdbc:postgresql://actual.example/petclinic")},
				{Name: new("SPRING_DATASOURCE_PASSWORD"), SecretRef: new("database-password")},
			},
			Command: []*string{new("java"), new("-Dspring.profiles.active=prod"), new("-jar"), new("/app/application.jar")},
			Args:    []*string{new("--spring.datasource.username=actual_app")},
		}}},
	}}
	input, err := deployedSpringInput(t.Context(), app, func(context.Context) (map[string]string, error) {
		return map[string]string{"database-password": "resolved-secret"}, nil
	})
	require.NoError(t, err)
	require.Equal(t, "resolved-secret", input.environment["SPRING_DATASOURCE_PASSWORD"])
	require.Equal(t, []string{"-Dspring.profiles.active=prod"}, input.jvm)
	require.Equal(t, []string{"--spring.datasource.username=actual_app"}, input.arguments)
	_, err = deployedSpringInput(t.Context(), app, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "database-password")
	app.Properties.Template.Containers[0].Command = []*string{new("sh"), new("-c"), new("custom-launch")}
	_, err = deployedSpringInput(t.Context(), app, nil)
	require.ErrorContains(t, err, "custom application launch")
}

func TestAutomaticDiscoveryBindsActualAttachedIdentityBeforeReturningFinalSelection(t *testing.T) {
	project, service := automaticPostgresProject(t)
	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	serverID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	identityID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/app"
	binding := &deploymentBinding{
		values: map[string]string{
			postgresServerIDEnv: serverID, postgresHostEnv: "actual.example",
			serviceIdentityIDEnv: identityID, serviceIdentityClientIDEnv: testSpringClientID,
		},
		resources: map[string]bool{
			strings.ToLower(serverID): true, strings.ToLower(serverID + "/databases/petclinic"): true,
			strings.ToLower(identityID): true,
		},
		app: &armappcontainers.ContainerApp{
			Identity: &armappcontainers.ManagedServiceIdentity{
				UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{
					identityID: {ClientID: new(testSpringClientID)},
				},
			},
			Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
					{Name: new("POSTGRES_URL"),
						Value: new("jdbc:postgresql://actual.example:5432/petclinic")},
					{Name: new("AZURE_CLIENT_ID"), Value: new(testSpringClientID)},
				}}},
			}},
		},
	}
	ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
	runtime := new(Runtime)
	resolved, err := runtime.resolveService(ctx, project, service, nil, binding.values, true)
	require.NoError(t, err)
	require.True(t, resolved.database.discovered)
	require.Equal(t, serverID, resolved.database.ServerResourceID)
	require.Equal(t, identityID, resolved.database.AppIdentity.ResourceID)
	require.Equal(t, "petclinic", binding.values[postgresDatabaseEnv])
	delete(binding.app.Identity.UserAssignedIdentities, identityID)
	_, err = runtime.resolveService(ctx, project, service, nil, binding.values, true)
	require.ErrorContains(t, err, "not attached")
}

func TestExplicitDatasourceChecksEffectiveApplicationSettings(t *testing.T) {
	project, service := automaticPostgresProject(t)
	binding := &deploymentBinding{
		values: map[string]string{postgresHostEnv: "actual.example", serviceIdentityClientIDEnv: testSpringClientID},
		app: &armappcontainers.ContainerApp{Properties: &armappcontainers.ContainerAppProperties{
			Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{
				Env: []*armappcontainers.EnvironmentVar{
					{Name: new("POSTGRES_URL"), Value: new("jdbc:postgresql://actual.example/petclinic")},
					{Name: new("AZURE_CLIENT_ID"), Value: new(testSpringClientID)},
				},
			}}},
		}},
	}
	ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
	expected := resolvedDatabase{
		Type: databasePostgreSQL, Authentication: databaseAuthenticationManagedIdentity,
		Name: "petclinic", Username: "petclinic_app",
	}
	runtime := new(Runtime)
	require.NoError(t, runtime.verifyExplicitDatasource(ctx, project, service, nil, expected, nil))
	for _, mutate := range []func(*resolvedDatabase){
		func(value *resolvedDatabase) { value.Name = "other" },
		func(value *resolvedDatabase) { value.Username = "other" },
		func(value *resolvedDatabase) { value.Type = databaseH2 },
		func(value *resolvedDatabase) { value.Authentication = databaseAuthenticationPassword },
	} {
		selected := expected
		mutate(&selected)
		require.Error(t, runtime.verifyExplicitDatasource(ctx, project, service, nil, selected, nil))
	}
	binding.values[serviceIdentityClientIDEnv] = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	require.Error(t, runtime.verifyExplicitDatasource(ctx, project, service, nil, expected, nil))
	require.Error(t, runtime.verifyExplicitDatasource(t.Context(), project, service, nil, expected, nil))
}

func TestDeployedDatasourceRejectsMountsBeforeReadingSecrets(t *testing.T) {
	app := &armappcontainers.ContainerApp{Properties: &armappcontainers.ContainerAppProperties{
		Template: &armappcontainers.Template{Containers: []*armappcontainers.Container{{
			VolumeMounts: []*armappcontainers.VolumeMount{{MountPath: new("/app/config")}},
		}}},
	}}
	_, err := deployedSpringInput(t.Context(), app, func(context.Context) (map[string]string, error) {
		t.Fatal("secret resolution must not precede local launch/configuration validation")
		return nil, nil
	})
	require.ErrorContains(t, err, "mounted application files")
}
