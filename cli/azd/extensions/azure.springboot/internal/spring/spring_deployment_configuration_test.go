// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
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
	store := &fakeEnvironmentStore{name: "dev", data: map[string]string{
		azureSubscriptionIDEnv: "subscription", "DATABASE_ADMIN_NAME": "developer@example.test",
	}}
	identities := &fakeDeveloperIdentityResolver{identity: &developerIdentity{
		ObjectID: testSpringClientID, UserAccessTenantID: testSpringClientID,
	}}
	runtime := &Runtime{environments: store, identities: identities, noPrompt: true}
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

func TestAutomaticPreProvisionDoesNotRequireRunnerImage(t *testing.T) {
	for _, key := range []string{
		databasePasswordEnv, databaseAppPasswordEnv, databaseAdministratorNameEnv,
		databaseAdministratorObjectIDEnv, "DATABASE_ADMIN_NAME", "DATABASE_ADMIN_OBJECT_ID",
	} {
		clearProcessValue(t, key)
	}
	project, _ := automaticPostgresProject(t)
	store := &fakeEnvironmentStore{name: "dev", data: map[string]string{azureSubscriptionIDEnv: "subscription"}}
	identities := &fakeDeveloperIdentityResolver{err: errors.New("expected identity resolution")}
	runtime := &Runtime{environments: store, identities: identities}

	err := runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project})

	require.ErrorContains(t, err, "expected identity resolution")
	require.Equal(t, 1, identities.calls)
	require.Empty(t, store.writes)
}

func TestH2PreProvisionDoesNotRequireBootstrap(t *testing.T) {
	project, _ := createMavenProject(t)
	store := &fakeEnvironmentStore{name: "dev", data: map[string]string{}}
	runtime := &Runtime{environments: store}

	require.NoError(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
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

func TestAutomaticDiscoveryBindsSystemAssignedIdentityWithoutClientID(t *testing.T) {
	project, service := automaticPostgresProject(t)
	propertiesPath := filepath.Join(project.Path, "src", "main", "resources", "application.properties")
	properties, err := os.ReadFile(propertiesPath)
	require.NoError(t, err)
	properties = []byte(strings.ReplaceAll(
		string(properties),
		"spring.datasource.hikari.data-source-properties[azure.clientId]=${AZURE_CLIENT_ID:}\n",
		"",
	))
	// #nosec G703 -- fixed path beneath a test-owned temporary project directory.
	require.NoError(t, os.WriteFile(propertiesPath, properties, 0o600))

	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	serverID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	appID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/Microsoft.App/containerApps/app"
	principalID := "22222222-3333-4444-5555-666666666666"
	binding := &deploymentBinding{
		values: map[string]string{
			postgresServerIDEnv: serverID, postgresHostEnv: "actual.example",
			containerAppIDEnv: appID, serviceIdentityPrincipalIDEnv: principalID,
		},
		resources: map[string]bool{
			strings.ToLower(serverID): true, strings.ToLower(serverID + "/databases/petclinic"): true,
			strings.ToLower(appID): true,
		},
		app: &armappcontainers.ContainerApp{
			ID: new(appID),
			Identity: &armappcontainers.ManagedServiceIdentity{
				Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
				PrincipalID: new(principalID),
			},
			Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
					{Name: new("POSTGRES_URL"),
						Value: new("jdbc:postgresql://actual.example:5432/petclinic")},
				}}},
			}},
		},
	}
	ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
	resolved, err := new(Runtime).resolveService(ctx, project, service, nil, binding.values, true)
	require.NoError(t, err)
	require.Equal(t, databaseAppIdentityTypeSystemAssigned, resolved.database.AppIdentity.Type)
	require.Equal(t, appID, resolved.database.AppIdentity.ResourceID)

	binding.app.Identity.PrincipalID = new("77777777-8888-9999-aaaa-bbbbbbbbbbbb")
	_, err = new(Runtime).resolveService(ctx, project, service, nil, binding.values, true)
	require.ErrorContains(t, err, "does not match")
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
		AppIdentity: &databaseAppIdentity{Type: databaseAppIdentityTypeUserAssigned},
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

func TestMultiModuleProjectResolvesPostgresAndDatabaseFreeServices(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mvnw"), []byte("wrapper"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".mvn", "wrapper", "maven-wrapper.properties"), []byte("props"), 0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project>
		<parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId></parent>
		<groupId>org.springframework.samples</groupId>
		<artifactId>spring-petclinic-multiservice</artifactId>
		<packaging>pom</packaging>
	</project>`), 0o600))

	restJavaDir := filepath.Join(root, "petclinic-rest", "src", "main", "java", "app")
	restResDir := filepath.Join(root, "petclinic-rest", "src", "main", "resources")
	webJavaDir := filepath.Join(root, "petclinic-web", "src", "main", "java", "web")
	webResDir := filepath.Join(root, "petclinic-web", "src", "main", "resources")
	for _, dir := range []string{restJavaDir, restResDir, webJavaDir, webResDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(restJavaDir, "App.java"), []byte(
		"package app;\nimport org.springframework.boot.SpringApplication;\n"+
			"public class App { public static void main(String[] args) { SpringApplication.run(App.class, args); } }\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(webJavaDir, "Web.java"), []byte(
		"package web;\nimport org.springframework.boot.SpringApplication;\n"+
			"public class Web { public static void main(String[] args) { SpringApplication.run(Web.class, args); } }\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "petclinic-rest", "pom.xml"), []byte(`<project>
		<parent>
			<groupId>org.springframework.samples</groupId>
			<artifactId>spring-petclinic-multiservice</artifactId>
			<relativePath>../pom.xml</relativePath>
		</parent>
		<artifactId>spring-petclinic-rest</artifactId>
		<dependencies>
			<dependency>
				<groupId>org.postgresql</groupId>
				<artifactId>postgresql</artifactId>
				<scope>runtime</scope>
			</dependency>
			<dependency>
				<groupId>com.azure</groupId>
				<artifactId>azure-identity-extensions</artifactId>
			</dependency>
		</dependencies>
	</project>`), 0o600))
	jdbcURL := "jdbc:postgresql://actual.example:5432/petclinic?" +
		"sslmode=verify-full&sslrootcert=/etc/ssl/certs/ca-certificates.crt&" +
		"authenticationPluginClassName=" + azurePostgreSQLPlugin + "&azure.managedIdentityEnabled=true"
	require.NoError(t, os.WriteFile(filepath.Join(restResDir, "application.properties"), []byte(
		"spring.datasource.url=${POSTGRES_URL:"+jdbcURL+"}\n"+
			"spring.datasource.username=petclinic_app\n",
	), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "petclinic-web", "pom.xml"), []byte(`<project>
		<parent>
			<groupId>org.springframework.samples</groupId>
			<artifactId>spring-petclinic-multiservice</artifactId>
			<relativePath>../pom.xml</relativePath>
		</parent>
		<artifactId>spring-petclinic-web</artifactId>
		<dependencies>
			<dependency>
				<groupId>org.springframework.boot</groupId>
				<artifactId>spring-boot-starter-webmvc</artifactId>
			</dependency>
		</dependencies>
	</project>`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(webResDir, "application.properties"), []byte(
		"server.port=8080\npetclinic.backend.url=${PETCLINIC_BACKEND_URL:http://localhost:9966/petclinic}\n",
	), 0o600))

	restSvc := &azdext.ServiceConfig{Name: "petclinic-rest", Host: SpringHost, RelativePath: "./petclinic-rest"}
	webSvc := &azdext.ServiceConfig{Name: "petclinic-web", Host: SpringHost, RelativePath: "./petclinic-web"}
	project := &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			"petclinic-rest": restSvc,
			"petclinic-web":  webSvc,
		},
	}

	runtime := new(Runtime)
	services, selected, err := runtime.resolveProject(t.Context(), project, map[string]string{}, true, false)
	require.NoError(t, err)
	require.Len(t, services, 2)
	require.Equal(t, databasePostgreSQL, selected.Type)

	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	serverID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	restAppID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/containerApps/petclinic-rest"
	webAppID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/containerApps/petclinic-web"
	principalID := "22222222-3333-4444-5555-666666666666"

	binding := &deploymentBinding{
		original: true,
		values: map[string]string{
			postgresServerIDEnv:           serverID,
			postgresHostEnv:               "actual.example",
			serviceIdentityPrincipalIDEnv: principalID,
			"CONTAINER_APP_ENVIRONMENT_ID": "/subscriptions/" + subscription +
				"/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/cae",
		},
		resources: map[string]bool{
			strings.ToLower(serverID):                          true,
			strings.ToLower(serverID + "/databases/petclinic"): true,
			strings.ToLower(restAppID):                         true,
			strings.ToLower(webAppID):                          true,
			strings.ToLower("/subscriptions/" + subscription +
				"/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/cae"): true,
		},
		apps: map[string]*armappcontainers.ContainerApp{
			"petclinic-rest": {
				ID: new(restAppID),
				Identity: &armappcontainers.ManagedServiceIdentity{
					Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
					PrincipalID: new(principalID),
				},
				Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
					Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
						{Name: new("SPRING_DATASOURCE_URL"), Value: new(jdbcURL)},
						{Name: new("SPRING_DATASOURCE_USERNAME"), Value: new("petclinic_app")},
					}}},
				}},
			},
			"petclinic-web": {
				ID: new(webAppID),
				Identity: &armappcontainers.ManagedServiceIdentity{
					Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
					PrincipalID: new("33333333-4444-5555-6666-777777777777"),
				},
				Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
					Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
						{Name: new("PETCLINIC_BACKEND_URL"), Value: new("https://petclinic-rest.example/petclinic")},
					}}},
				}},
			},
		},
	}

	ctx := context.WithValue(t.Context(), deploymentBindingKey{}, binding)
	services, selected, err = runtime.resolveProject(ctx, project, binding.values, false, true)
	require.NoError(t, err)
	require.Len(t, services, 2)
	require.Equal(t, databasePostgreSQL, selected.Type)
	require.Equal(t, restAppID, selected.AppIdentity.ResourceID)
}

func TestMultiDatabasePostgresServicesReconcileAndEnforcePerServiceAccess(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mvnw"), []byte("wrapper"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, ".mvn", "wrapper", "maven-wrapper.properties"), []byte("props"), 0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project>
		<parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId></parent>
		<groupId>org.springframework.samples</groupId>
		<artifactId>petclinic-microservices</artifactId>
		<packaging>pom</packaging>
	</project>`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "infra"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "infra", "main.parameters.json"), []byte(`{
		"parameters": {
			"databaseAdministratorName": {"value": "${DATABASE_ADMIN_NAME=azd-preprovision-pending}"},
			"databaseAdministratorObjectId": {"value": "${DATABASE_ADMIN_OBJECT_ID=00000000-0000-0000-0000-000000000000}"}
		}
	}`), 0o600))

	writeModule := func(name, depsXML, appYML string) *azdext.ServiceConfig {
		javaDir := filepath.Join(root, name, "src", "main", "java", "svc")
		resDir := filepath.Join(root, name, "src", "main", "resources")
		require.NoError(t, os.MkdirAll(javaDir, 0o755))
		require.NoError(t, os.MkdirAll(resDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(javaDir, "App.java"), []byte(
			"package svc;\nimport org.springframework.boot.SpringApplication;\n"+
				"public class App { public static void main(String[] a) { SpringApplication.run(App.class, a); } }\n",
		), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(root, name, "pom.xml"), []byte(`<project>
			<parent>
				<groupId>org.springframework.samples</groupId>
				<artifactId>petclinic-microservices</artifactId>
				<relativePath>../pom.xml</relativePath>
			</parent>
			<artifactId>`+name+`</artifactId>
			<dependencies>`+depsXML+`</dependencies>
		</project>`), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(resDir, "application.yml"), []byte(appYML), 0o600))
		return &azdext.ServiceConfig{Name: name, Host: SpringHost, RelativePath: "./" + name}
	}

	pgDeps := `<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>` +
		`<dependency><groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId></dependency>`
	webDeps := `<dependency><groupId>org.springframework.boot</groupId>` +
		`<artifactId>spring-boot-starter-webmvc</artifactId></dependency>`
	configImportYML := "spring:\n  config:\n    import: optional:configserver:http://localhost:8888/\n"

	customersSvc := writeModule("customers-service", pgDeps, configImportYML)
	vetsSvc := writeModule("vets-service", pgDeps, configImportYML)
	gatewaySvc := writeModule("api-gateway", webDeps, configImportYML)

	project := &azdext.ProjectConfig{
		Path:  root,
		Infra: &azdext.InfraOptions{Provider: "bicep", Path: "infra", Module: "main"},
		Services: map[string]*azdext.ServiceConfig{
			"customers-service": customersSvc,
			"vets-service":      vetsSvc,
			"api-gateway":       gatewaySvc,
		},
	}

	subscription := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	adminOID := "11111111-2222-3333-4444-555555555555"
	tenantID := "99999999-8888-7777-6666-555555555555"
	serverID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	caeID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/managedEnvironments/cae"
	customersAppID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/containerApps/customers-service"
	vetsAppID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/containerApps/vets-service"
	gatewayAppID := "/subscriptions/" + subscription + "/resourceGroups/rg/providers/" +
		"Microsoft.App/containerApps/api-gateway"
	customersPrincipal := "22222222-3333-4444-5555-666666666666"
	vetsPrincipal := "33333333-4444-5555-6666-777777777777"

	mkJDBC := func(dbName string) string {
		return "jdbc:postgresql://pg.example:5432/" + dbName + "?" +
			"sslmode=verify-full&sslrootcert=/etc/ssl/certs/ca-certificates.crt&" +
			"authenticationPluginClassName=" + azurePostgreSQLPlugin + "&azure.managedIdentityEnabled=true"
	}

	envValues := map[string]string{
		azureSubscriptionIDEnv:           subscription,
		azureResourceGroupEnv:            "rg",
		"DATABASE_ADMIN_NAME":            "admin@example.com",
		"DATABASE_ADMIN_OBJECT_ID":       adminOID,
		databaseAdministratorNameEnv:     "admin@example.com",
		databaseAdministratorObjectIDEnv: adminOID,
		databaseUserAccessTenantIDEnv:    tenantID,
		postgresServerIDEnv:              serverID,
		postgresHostEnv:                  "pg.example",
		"CONTAINER_APP_ENVIRONMENT_ID":   caeID,
	}

	makeBinding := func(vetsRole string, base map[string]string) *deploymentBinding {
		merged := maps.Clone(envValues)
		maps.Copy(merged, base)
		return &deploymentBinding{
			original:  true,
			automatic: true,
			values:    merged,
			resources: map[string]bool{
				strings.ToLower(serverID):                          true,
				strings.ToLower(serverID + "/databases/customers"): true,
				strings.ToLower(serverID + "/databases/vets"):      true,
				strings.ToLower(customersAppID):                    true,
				strings.ToLower(vetsAppID):                         true,
				strings.ToLower(gatewayAppID):                      true,
				strings.ToLower(caeID):                             true,
			},
			apps: map[string]*armappcontainers.ContainerApp{
				"customers-service": {
					ID: new(customersAppID),
					Identity: &armappcontainers.ManagedServiceIdentity{
						Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
						PrincipalID: new(customersPrincipal),
					},
					Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
						Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
							{Name: new("SPRING_DATASOURCE_URL"), Value: new(mkJDBC("customers"))},
							{Name: new("SPRING_DATASOURCE_USERNAME"), Value: new("customers_app")},
						}}},
					}},
				},
				"vets-service": {
					ID: new(vetsAppID),
					Identity: &armappcontainers.ManagedServiceIdentity{
						Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
						PrincipalID: new(vetsPrincipal),
					},
					Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
						Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{
							{Name: new("SPRING_DATASOURCE_URL"), Value: new(mkJDBC("vets"))},
							{Name: new("SPRING_DATASOURCE_USERNAME"), Value: new(vetsRole)},
						}}},
					}},
				},
				"api-gateway": {
					ID: new(gatewayAppID),
					Identity: &armappcontainers.ManagedServiceIdentity{
						Type:        new(armappcontainers.ManagedServiceIdentityTypeSystemAssigned),
						PrincipalID: new("44444444-5555-6666-7777-888888888888"),
					},
					Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
						Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{}}},
					}},
				},
			},
		}
	}

	// 1. PreProvision succeeds with configserver-imported modules before deployment.
	store := &fakeEnvironmentStore{name: "dev", data: maps.Clone(envValues)}
	identities := &fakeDeveloperIdentityResolver{
		identity: &developerIdentity{ObjectID: adminOID, UserAccessTenantID: tenantID},
	}
	var reconciled []string
	liveVerifier := &azureDeploymentOutputVerifier{}
	runtime := &Runtime{
		environments: store,
		identities:   identities,
		outputs: &delegatingOutputVerifier{
			read: func(v map[string]string) (*deploymentBinding, error) { return makeBinding("vets_app", v), nil },
			verify: func(ctx context.Context, db resolvedDatabase, v map[string]string) (*deploymentBinding, error) {
				return liveVerifier.Verify(ctx, db, v)
			},
		},
		reconcile: func(context.Context, map[string]string) error { return nil },
		prepareNetwork: func(context.Context, *deploymentBinding) (func(context.Context) error, error) {
			return func(context.Context) error { return nil }, nil
		},
		configureAccess: func(_ context.Context, _ string, db resolvedDatabase, v map[string]string) error {
			reconciled = append(
				reconciled,
				db.Name+":"+db.Username+":"+v[serviceIdentityPrincipalIDEnv],
			)
			return nil
		},
	}

	require.NoError(t, runtime.PreProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))

	// 2. PostProvision reconciles both PostgreSQL services with their respective principals and skips api-gateway.
	require.NoError(t, runtime.PostProvision(t.Context(), &azdext.ProjectEventArgs{Project: project}))
	require.Equal(t, []string{
		"customers:customers_app:" + customersPrincipal,
		"vets:vets_app:" + vetsPrincipal,
	}, reconciled)
	require.NotEmpty(t, store.data[serviceProvisionedAccessEnvKey("customers-service")])
	require.NotEmpty(t, store.data[serviceProvisionedAccessEnvKey("vets-service")])
	require.True(t, strings.HasPrefix(store.data[provisionedDatabaseAccessEnv], "multi:"))

	// 3. PreDeploy passes for all 3 services using their per-service fingerprints.
	for _, svc := range []*azdext.ServiceConfig{customersSvc, vetsSvc, gatewaySvc} {
		require.NoError(t, runtime.PreDeploy(t.Context(), &azdext.ServiceEventArgs{
			Project: project,
			Service: svc,
		}))
	}

	// 4. Mutating one service's database access after provisioning fails per-service immutability.
	mutatedBinding := makeBinding("customers_app", store.data)
	mutatedCtx := context.WithValue(t.Context(), deploymentBindingKey{}, mutatedBinding)
	_, _, err := runtime.resolveProject(mutatedCtx, project, mutatedBinding.values, false, true)
	require.ErrorContains(t, err, "changes the provisioned PostgreSQL access identity")

	// 5. Role collision across distinct identities on initial provision fails closed.
	collisionBinding := makeBinding("customers_app", nil)
	collisionCtx := context.WithValue(t.Context(), deploymentBindingKey{}, collisionBinding)
	_, _, err = runtime.resolveProject(collisionCtx, project, collisionBinding.values, false, true)
	require.ErrorContains(t, err, "cannot share one PostgreSQL role login")
}

type delegatingOutputVerifier struct {
	read   func(map[string]string) (*deploymentBinding, error)
	verify func(context.Context, resolvedDatabase, map[string]string) (*deploymentBinding, error)
}

func (v *delegatingOutputVerifier) Read(_ context.Context, values map[string]string) (*deploymentBinding, error) {
	return v.read(values)
}

func (v *delegatingOutputVerifier) Verify(
	ctx context.Context, db resolvedDatabase, values map[string]string,
) (*deploymentBinding, error) {
	return v.verify(ctx, db, values)
}
