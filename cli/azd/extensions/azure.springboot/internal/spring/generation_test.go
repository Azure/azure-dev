// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestGenerateInfrastructure(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind ResourceKind
		sql  string
	}{
		{"embedded", "", ""},
		{"mysql", MySQL, "Microsoft.DBforMySQL/flexibleServers"},
		{"postgresql", PostgreSQL, "Microsoft.DBforPostgreSQL/flexibleServers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "inventory", JavaVersion: 21, JarName: "inventory-1.0.jar", Port: 9090,
				BuildDirectory: "target",
				Profiles:       []string{"release"}, MavenWrapper: true,
				ExternalConfigurations: []string{"config/application-release.properties"},
			}

			if tt.kind != "" {
				a.Requirements = []Requirement{{
					Kind: tt.kind, Name: "inventory", Authentication: "password",
					JDBCOptions: "connectTimeout=10",
				}}
			}
			plan, err := Generate(t.Context(), a)
			require.NoError(t, err)
			config, err := project.Parse(t.Context(), string(plan.Files["azure.yaml"]))
			require.NoError(t, err)
			require.Contains(t, config.Services, "inventory-app")
			require.Equal(t, project.ServiceLanguageDocker, config.Services["inventory-app"].Language)
			require.Equal(t, "application", config.Services["inventory-app"].Module)
			require.Empty(t, config.Resources)
			var parameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
			require.Equal(t, float64(9090), parameters.Parameters["port"].Value)
			var deploymentParameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/application.parameters.json"], &deploymentParameters))
			require.Equal(t, parameters.Parameters["port"], deploymentParameters.Parameters["port"])
			require.Equal(t, parameters.Parameters["appImage"], deploymentParameters.Parameters["appImage"])
			require.Equal(t, parameters.Parameters["databaseEngine"], deploymentParameters.Parameters["engine"])
			require.Equal(t, "${SPRING_CONTAINER_APP_ENVIRONMENT_ID}",
				deploymentParameters.Parameters["environmentId"].Value)
			require.Equal(t, false, deploymentParameters.Parameters["environmentBindings"].Value)
			require.Contains(t, string(plan.Files["infra/main.bicep"]), "output SPRING_CONTAINER_APP_ENVIRONMENT_ID")
			require.Contains(t, string(plan.Files["infra/application.bicep"]), "targetPort: placeholder ? 80 : port")
			require.Contains(t, string(plan.Files["infra/application.bicep"]),
				"{ name: 'SPRING_PROFILES_DEFAULT', value: 'none' }")
			require.Contains(t, string(plan.Files["infra/application.bicep"]),
				"{ name: 'SERVER_ADDRESS', value: '0.0.0.0' }")
			var bicep strings.Builder
			for name, content := range plan.Files {
				if strings.HasSuffix(name, ".bicep") {
					bicep.Write(content)
				}
			}
			if tt.kind == "" {
				require.Equal(t, "none", parameters.Parameters["databaseEngine"].Value)
			} else {
				require.Equal(t, string(tt.kind), parameters.Parameters["databaseEngine"].Value)
				require.Equal(t, "inventory", parameters.Parameters["databaseName"].Value)
				require.Contains(t, bicep.String(), tt.sql)
				require.Contains(t, bicep.String(), "secretRef")
				require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]), "_PASSWORD}")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "_PASSWORD}")
			}
			require.Contains(t, bicep.String(), "@onlyIfNotExists()")
			require.NotContains(t, bicep.String(), "br/public:")
			require.NotContains(t, bicep.String(), "Microsoft.KeyVault")
			require.NotContains(t, plan.Files, ".azd-spring/bootstrap-postgresql-managed-identity.py")
			require.NotContains(t, plan.Files, ".azd-spring/database.json")
			require.NotContains(t, plan.Files, ".azd-spring/requirements.txt")
			if tt.kind == "" {
				require.NotContains(t, plan.Files, ".azd-spring/prepare-database-password.ps1")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "hooks:")
			} else {
				require.Contains(t, plan.Files, ".azd-spring/prepare-database-password.ps1")
				require.Contains(t, string(plan.Files["azure.yaml"]),
					"run: .azd-spring/prepare-database-password.ps1")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "python")
			}
			docker := string(plan.Files[".azd-spring/Dockerfile"])
			require.Contains(t, docker, "eclipse-temurin:21-jre")
			require.Contains(t, docker, "target/inventory-1.0.jar")
			require.Contains(t, docker, "./mvnw")
			require.Contains(t, docker, "-Dmaven.test.skip=true package")
			require.NotContains(t, docker, "-DskipTests")
			require.Contains(t, docker, "/app/.azd-spring/application.properties")
			require.Contains(t, docker,
				`["/workspace/config/application-release.properties","/app/config/application-release.properties"]`)
			directory := t.TempDir()
			require.NoError(t, plan.Write(t.Context(), directory))
			for name, content := range plan.Files {
				data, err := fs.ReadFile(os.DirFS(directory), name)
				require.NoError(t, err)
				require.Equal(t, content, data)
			}
			require.ErrorContains(t, plan.Write(t.Context(), directory), "already exists")
		})
	}
}

func TestGenerateApplicationInputs(t *testing.T) {
	application := &Application{
		Name: "inventory", JavaVersion: 21, JarName: "inventory-1.0.jar", Port: 8080,
		BuildDirectory: "target",
		ApplicationInputs: []ApplicationInput{
			{Name: "FRONTEND_URL", Kind: ApplicationInputValue},
			{Name: "JWT_SECRET", Kind: ApplicationInputSecret},
		},
	}
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)

	var parameters struct {
		Parameters map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
	var values map[string]string
	require.NoError(t, json.Unmarshal(parameters.Parameters["applicationValues"].Value, &values))
	require.Equal(t, map[string]string{
		"FRONTEND_URL": "${FRONTEND_URL}",
	}, values)
	var secrets map[string]struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(parameters.Parameters["applicationSecrets"].Value, &secrets))
	require.Len(t, secrets, 1)
	for name, secret := range secrets {
		require.Regexp(t, `^app-[0-9a-f]{12}$`, name)
		require.Equal(t, "JWT_SECRET", secret.Name)
		require.Equal(t, "${JWT_SECRET}", secret.Value)
	}

	var applicationParameters struct {
		Parameters map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(
		plan.Files["infra/application.parameters.json"], &applicationParameters))
	require.JSONEq(t, string(parameters.Parameters["applicationValues"].Value),
		string(applicationParameters.Parameters["applicationValues"].Value))
	require.JSONEq(t, string(parameters.Parameters["applicationSecrets"].Value),
		string(applicationParameters.Parameters["applicationSecrets"].Value))

	main := string(plan.Files["infra/main.bicep"])
	require.Contains(t, main, "@secure()\nparam applicationSecrets object = {}")
	applicationBicep := string(plan.Files["infra/application.bicep"])
	require.Contains(t, applicationBicep, "var applicationSecretEntries")
	require.Contains(t, applicationBicep, "secretRef: input.key")
	require.Contains(t, applicationBicep, "applicationValueEnvironment")
	require.NotContains(t, string(plan.Files["azure.yaml"]), "JWT_SECRET")
}

func TestGeneratedDockerIgnoreAllowsCredentialNamedConfiguration(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.profiles.active=credentials\n")
	const configuration = "config/application-credentials.properties"
	writeFixture(t, directory, configuration,
		"spring.datasource.url=jdbc:postgresql://localhost/catalog\n"+
			"app.credential-policy=external\n")
	application, err := Analyze(t.Context(), directory,
		staticResolver{model: effectiveModel(t, directory, allDrivers())}, envLookup(nil))
	require.NoError(t, err)
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	require.Contains(t, string(plan.Files[".azd-spring/Dockerfile"]),
		`["/workspace/config/application-credentials.properties","/app/config/application-credentials.properties"]`)
	ignore := string(plan.Files[".azd-spring/Dockerfile.dockerignore"])
	require.NotContains(t, ignore, "credentials")
}

func TestMemoryDatabaseParametersAreOptional(t *testing.T) {
	plan, err := Generate(t.Context(), &Application{
		Name: "inventory", JavaVersion: 21, JarName: "inventory.jar", Port: 9090,
		BuildDirectory: "target",
	})
	require.NoError(t, err)
	for _, tt := range []struct {
		module     string
		parameters []string
	}{
		{"main", []string{"databaseName", "databasePassword"}},
		{"application", []string{"databaseName", "host", "password"}},
	} {
		t.Run(tt.module, func(t *testing.T) {
			template := string(plan.Files["infra/"+tt.module+".bicep"])
			for _, parameter := range tt.parameters {
				require.Contains(t, template, "param "+parameter+" string = ''")
			}
			var parameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/"+tt.module+".parameters.json"], &parameters))
			require.Empty(t, parameters.Parameters["databaseName"].Value)
		})
	}
}

func TestGenerateFoundryInfrastructure(t *testing.T) {
	application := &Application{
		Name: "foundry-chat", JavaVersion: 21, JarName: "foundry-chat.jar", Port: 8080,
		BuildDirectory: "target",
		Foundry: &FoundryRequirement{
			ChatDeploymentName: "gpt-5.6-sol", Authentication: "apiKey",
		},
	}
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)

	var parameters struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
	require.Equal(t, true, parameters.Parameters["foundryEnabled"].Value)
	require.Equal(t, "apiKey", parameters.Parameters["foundryAuthentication"].Value)
	require.Equal(t, true, parameters.Parameters["foundryChatEnabled"].Value)
	require.Equal(t, "gpt-5.6-sol", parameters.Parameters["foundryChatDeploymentName"].Value)
	require.Equal(t, "gpt-5.6-sol", parameters.Parameters["foundryChatModelName"].Value)
	require.Equal(t, "2026-07-09", parameters.Parameters["foundryChatModelVersion"].Value)
	require.Equal(t, "GlobalStandard", parameters.Parameters["foundryChatModelSku"].Value)
	require.Equal(t, float64(1), parameters.Parameters["foundryChatModelCapacity"].Value)
	require.Equal(t, false, parameters.Parameters["foundryEmbeddingEnabled"].Value)

	foundry := string(plan.Files["infra/foundry.bicep"])
	require.Contains(t, foundry, "Microsoft.CognitiveServices/accounts@2025-06-01")
	require.Contains(t, foundry, "kind: 'AIServices'")
	require.Contains(t, foundry, "identity: { type: 'SystemAssigned' }")
	require.Contains(t, foundry, "allowProjectManagement: true")
	require.Contains(t, foundry, "disableLocalAuth: authentication == 'managedIdentity'")
	require.Contains(t, foundry, "defaultAction: 'Deny'")
	require.Contains(t, foundry, "for ip in union(outboundIps, [])")
	require.Contains(t, foundry, "Microsoft.CognitiveServices/accounts/deployments@2025-06-01")
	require.Contains(t, foundry, "@batchSize(1)")
	require.Contains(t, foundry, "resource modelDeployments")
	require.Contains(t, foundry, "for deployment in deployments")
	require.Contains(t, foundry, "Microsoft.Authorization/roleAssignments@2022-04-01")
	require.Contains(t, foundry, "5e0bd9bd-7b93-4f28-af87-19fc36ad61bd")
	require.NotContains(t, foundry, "accounts/projects")

	main := string(plan.Files["infra/main.bicep"])
	require.Contains(t, main, "outboundIps: bootstrap.outputs.outboundIps")
	require.Contains(t, main, "applicationPrincipalId: bootstrap.outputs.principalId")
	require.Contains(t, main, "output SPRING_FOUNDRY_ACCOUNT_NAME")
	applicationTemplate := string(plan.Files["infra/application.bicep"])
	require.Contains(t, applicationTemplate, "foundry!.listKeys().key1")
	require.Contains(t, applicationTemplate, "secretRef: 'foundry-api-key'")
	require.Contains(t, applicationTemplate, "SPRING_AI_AZURE_OPENAI_ENDPOINT")
	require.Contains(t, applicationTemplate, "SPRING_AI_AZURE_OPENAI_OPENAIAPIKEY")
	require.Contains(t, applicationTemplate, "SPRING_AI_AZURE_OPENAI_APIKEY")
	require.Contains(t, applicationTemplate, "SPRING_AI_AZURE_OPENAI_CHAT_OPTIONS_DEPLOYMENTNAME")
	require.Contains(t, applicationTemplate, "AZURE_OPENAI_ENDPOINT")
	require.NotContains(t, string(plan.Files["infra/main.parameters.json"]), "foundry-api-key")
	require.NotContains(t, string(plan.Files["infra/application.parameters.json"]), "AZURE_OPENAI_API_KEY")
	binding := string(plan.Files[".azd-spring/application-azd.properties"])
	require.Contains(t, binding, "spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}")
	require.Contains(t, binding, "spring.ai.azure.openai.api-key=${AZURE_OPENAI_API_KEY}")
	require.Contains(t, binding, "spring.ai.azure.openai.openai-api-key=\n")
	require.Contains(t, binding,
		"spring.ai.azure.openai.chat.options.deployment-name=${AZURE_OPENAI_CHAT_DEPLOYMENT_NAME}")
}

func TestGenerateFoundryManagedIdentityInfrastructure(t *testing.T) {
	application := &Application{
		Name: "foundry-chat", JavaVersion: 21, JarName: "foundry-chat.jar", Port: 8080,
		BuildDirectory: "target",
		Foundry: &FoundryRequirement{
			ChatDeploymentName: "gpt-5.6-sol", Authentication: "managedIdentity",
		},
	}
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)

	var parameters struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
	require.Equal(t, "managedIdentity", parameters.Parameters["foundryAuthentication"].Value)

	foundry := string(plan.Files["infra/foundry.bicep"])
	require.Contains(t, foundry, "disableLocalAuth: authentication == 'managedIdentity'")
	require.Contains(t, foundry, "principalId: applicationPrincipalId")
	require.Contains(t, foundry, "roleDefinitionId: openAIUserRoleId")
	applicationTemplate := string(plan.Files["infra/application.bicep"])
	require.Contains(t, applicationTemplate,
		"var foundryUsesApiKey = foundryEnabled && foundryAuthentication == 'apiKey'")
	require.Contains(t, applicationTemplate, "foundryUsesApiKey ? foundry!.listKeys().key1 : ''")
	binding := string(plan.Files[".azd-spring/application-azd.properties"])
	require.Contains(t, binding, "spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}")
	require.NotContains(t, binding, "spring.ai.azure.openai.api-key")
	require.Contains(t, binding, "spring.ai.azure.openai.openai-api-key=\n")
	require.Contains(t, binding,
		"spring.ai.azure.openai.chat.options.deployment-name=${AZURE_OPENAI_CHAT_DEPLOYMENT_NAME}")
}

func TestGenerateFoundryEmbeddingInfrastructure(t *testing.T) {
	application := &Application{
		Name: "foundry-embedding", JavaVersion: 21, JarName: "foundry-embedding.jar", Port: 8080,
		BuildDirectory: "target",
		Foundry: &FoundryRequirement{
			EmbeddingDeploymentName: "text-embedding-3-small", Authentication: "apiKey",
		},
	}
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)

	var parameters struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
	require.Equal(t, true, parameters.Parameters["foundryEnabled"].Value)
	require.Equal(t, false, parameters.Parameters["foundryChatEnabled"].Value)
	require.Equal(t, true, parameters.Parameters["foundryEmbeddingEnabled"].Value)
	require.Equal(t, "text-embedding-3-small",
		parameters.Parameters["foundryEmbeddingDeploymentName"].Value)
	require.Equal(t, "text-embedding-3-small", parameters.Parameters["foundryEmbeddingModelName"].Value)
	require.Equal(t, "1", parameters.Parameters["foundryEmbeddingModelVersion"].Value)
	require.Equal(t, "GlobalStandard", parameters.Parameters["foundryEmbeddingModelSku"].Value)
	require.Equal(t, float64(1), parameters.Parameters["foundryEmbeddingModelCapacity"].Value)

	binding := string(plan.Files[".azd-spring/application-azd.properties"])
	require.NotContains(t, binding, "spring.ai.azure.openai.chat.options.deployment-name")
	require.Contains(t, binding,
		"spring.ai.azure.openai.embedding.options.deployment-name="+
			"${AZURE_OPENAI_EMBEDDING_DEPLOYMENT_NAME}")
	template := string(plan.Files["infra/application.bicep"])
	require.Contains(t, template, "SPRING_AI_AZURE_OPENAI_EMBEDDING_OPTIONS_DEPLOYMENTNAME")
}

func TestGenerateFoundryUsesSpringEnvironmentNamesWithExistingDockerfile(t *testing.T) {
	application := &Application{
		Name: "foundry-chat", JavaVersion: 21, Port: 8080, Dockerfile: "Dockerfile",
		Foundry: &FoundryRequirement{
			ChatDeploymentName: "chat", EmbeddingDeploymentName: "embeddings", Authentication: "apiKey",
		},
	}
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	require.NotContains(t, plan.Files, ".azd-spring/application-azd.properties")

	var parameters struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/application.parameters.json"], &parameters))
	require.Equal(t, true, parameters.Parameters["environmentBindings"].Value)

	template := string(plan.Files["infra/application.bicep"])
	require.Contains(t, template, "{ name: 'SPRING_AI_AZURE_OPENAI_APIKEY', secretRef: 'foundry-api-key' }")
	require.Contains(t, template, "{ name: 'SPRING_AI_AZURE_OPENAI_OPENAIAPIKEY', value: '' }")
	require.Contains(t, template,
		"name: 'SPRING_AI_AZURE_OPENAI_CHAT_OPTIONS_DEPLOYMENTNAME'")
	require.Contains(t, template, "value: foundryChatDeploymentName")
	require.Contains(t, template,
		"name: 'SPRING_AI_AZURE_OPENAI_EMBEDDING_OPTIONS_DEPLOYMENTNAME'")
	require.Contains(t, template, "value: foundryEmbeddingDeploymentName")
}

func TestGenerateRejectsInvalidExternalDrivers(t *testing.T) {
	for _, driver := range []string{
		"com.mysql.cj.jdbc.Driver", "example.CustomDriver", "org.postgresql.Driver\nunexpected.setting=true",
	} {
		t.Run(driver, func(t *testing.T) {
			plan, err := Generate(t.Context(), &Application{
				Name: "catalog", JavaVersion: 17, JarName: "catalog.jar", BuildDirectory: "target", Port: 8080,
				Requirements: []Requirement{{
					Kind: PostgreSQL, Name: "catalog", Authentication: "password", Driver: driver,
				}},
			})
			require.ErrorContains(t, err, "configured JDBC driver does not match")
			require.Nil(t, plan)
		})
	}
}

func TestDestinationOwnership(t *testing.T) {
	for _, name := range []string{"azure.yaml", "azure.yml", "infra", ".azd-spring"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, name, "user-owned")
			require.ErrorContains(t, CheckDestination(directory), "already exists")
			plan := &Plan{Files: map[string][]byte{"azure.yaml": []byte("generated")}}
			require.ErrorContains(t, plan.Write(t.Context(), directory), "already exists")
			if name != "azure.yaml" {
				_, err := os.Stat(filepath.Join(directory, "azure.yaml"))
				require.ErrorIs(t, err, fs.ErrNotExist)
			}
			data, err := fs.ReadFile(os.DirFS(directory), name)
			require.NoError(t, err)
			require.Equal(t, "user-owned", string(data))
		})
	}
}

func TestDestinationDockerInputs(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dockerfile bool
		ignore     bool
		reject     bool
	}{
		{name: "generated Dockerfile"},
		{name: "root ignore only", ignore: true, reject: true},
		{name: "application Dockerfile", dockerfile: true},
		{name: "application Dockerfile with root ignore", dockerfile: true, ignore: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			if tt.dockerfile {
				writeFixture(t, directory, "Dockerfile", "FROM scratch\n")
			}
			if tt.ignore {
				writeFixture(t, directory, ".dockerignore", "private/\n")
			}
			err := CheckDestination(directory)
			if tt.reject {
				require.ErrorContains(t, err, ".dockerignore requires an application-owned root Dockerfile")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWriteRejectsRootDockerignoreAppearingAfterPreflight(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, CheckDestination(directory))
	writeFixture(t, directory, ".dockerignore", "private/\n")
	plan := &Plan{Files: map[string][]byte{
		"azure.yaml":                          []byte("name: catalog\n"),
		".azd-spring/Dockerfile":              []byte("FROM scratch\nCOPY . .\n"),
		".azd-spring/Dockerfile.dockerignore": []byte(".azure\n"),
		"infra/main.bicep":                    []byte("targetScope = 'subscription'\n"),
	}}
	require.ErrorContains(t, plan.Write(t.Context(), directory),
		".dockerignore requires an application-owned root Dockerfile")
	for _, name := range []string{"azure.yaml", ".azd-spring", "infra"} {
		_, err := os.Stat(filepath.Join(directory, name))
		require.ErrorIs(t, err, fs.ErrNotExist)
	}
	ignore, err := fs.ReadFile(os.DirFS(directory), ".dockerignore")
	require.NoError(t, err)
	require.Equal(t, "private/\n", string(ignore))
}

func TestGenerateUsesExistingDockerfile(t *testing.T) {
	a := &Application{
		Name: "catalog", JavaVersion: 17, Port: 8080, Dockerfile: "Dockerfile",
		Requirements: []Requirement{{
			Kind: PostgreSQL, Name: "catalog", Authentication: "password",
			JDBCOptions: "sslmode=require", Driver: "org.postgresql.Driver",
		}},
	}
	plan, err := Generate(t.Context(), a)
	require.NoError(t, err)
	config, err := project.Parse(t.Context(), string(plan.Files["azure.yaml"]))
	require.NoError(t, err)
	require.Equal(t, "Dockerfile", config.Services["catalog-app"].Docker.Path)
	require.NotContains(t, plan.Files, ".azd-spring/Dockerfile")
	require.NotContains(t, plan.Files, ".azd-spring/Dockerfile.dockerignore")
	require.NotContains(t, plan.Files, ".azd-spring/application-azd.properties")
	var parameters struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(plan.Files["infra/application.parameters.json"], &parameters))
	require.Equal(t, true, parameters.Parameters["environmentBindings"].Value)
	require.Equal(t, "sslmode=require", parameters.Parameters["jdbcOptions"].Value)
	require.Equal(t, "org.postgresql.Driver", parameters.Parameters["driver"].Value)
	template := string(plan.Files["infra/application.bicep"])
	require.Contains(t, template, "SPRING_DATASOURCE_URL")
	require.Contains(t, template, "SPRING_DATASOURCE_PASSWORD")

	directory := t.TempDir()
	writeFixture(t, directory, "Dockerfile", "FROM eclipse-temurin:17-jre\n")
	writeFixture(t, directory, ".dockerignore", "private/\n")
	require.NoError(t, plan.Write(t.Context(), directory))
	dockerfile, err := fs.ReadFile(os.DirFS(directory), "Dockerfile")
	require.NoError(t, err)
	require.Equal(t, "FROM eclipse-temurin:17-jre\n", string(dockerfile))
	ignore, err := fs.ReadFile(os.DirFS(directory), ".dockerignore")
	require.NoError(t, err)
	require.Equal(t, "private/\n", string(ignore))
}

func TestWriteRejectsDockerfileStateChange(t *testing.T) {
	for _, tt := range []struct {
		name        string
		application *Application
		create      bool
	}{
		{"appeared", &Application{}, true},
		{"removed", &Application{Dockerfile: "Dockerfile"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory := t.TempDir()
			if tt.create {
				writeFixture(t, directory, "Dockerfile", "FROM scratch\n")
			}
			plan := &Plan{
				Application: tt.application,
				Files:       map[string][]byte{"azure.yaml": []byte("name: catalog\n")},
			}
			require.ErrorContains(t, plan.Write(t.Context(), directory), "after analysis")
			_, err := os.Stat(filepath.Join(directory, "azure.yaml"))
			require.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

func TestSharedTemplateAuthenticationMatrix(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind ResourceKind
		auth string
	}{
		{"memory", "", "password"},
		{"mysql-password", MySQL, "password"},
		{"postgres-password", PostgreSQL, "password"},
		{"postgres-identity", PostgreSQL, "managedIdentity"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "catalog", JavaVersion: 17, Port: 8080, JarName: "catalog.jar",
				BuildDirectory: "target",
				Profiles:       []string{"cloud"}, IdentityExtensions: true,
			}
			if tt.kind != "" {
				a.Requirements = []Requirement{{
					Kind: tt.kind, Name: "catalog", Authentication: tt.auth, JDBCOptions: "sslmode=require",
				}}
			}
			plan, err := Generate(t.Context(), a)
			require.NoError(t, err)
			var parameters struct {
				Parameters map[string]struct {
					Value any `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(plan.Files["infra/main.parameters.json"], &parameters))
			engine := string(tt.kind)
			if engine == "" {
				engine = "none"
			}
			require.Equal(t, engine, parameters.Parameters["databaseEngine"].Value)
			require.Equal(t, tt.auth, parameters.Parameters["databaseAuth"].Value)
			config, err := project.Parse(t.Context(), string(plan.Files["azure.yaml"]))
			require.NoError(t, err)
			require.Len(t, config.Services, 1)
			require.Contains(t, config.Services, "catalog-app")
			binding := string(plan.Files[".azd-spring/application-azd.properties"])
			if tt.auth == "managedIdentity" {
				require.Contains(t, plan.Files, ".azd-spring/bootstrap-postgresql-managed-identity.py")
				require.Contains(t, plan.Files, ".azd-spring/database.json")
				require.Contains(t, plan.Files, ".azd-spring/requirements.txt")
				require.NotContains(t, plan.Files, ".azd-spring/prepare-database-password.ps1")
				var settings map[string]string
				require.NoError(t, json.Unmarshal(plan.Files[".azd-spring/database.json"], &settings))
				require.Equal(t, "postgresql", settings["engine"])
				require.Equal(t, "managedIdentity", settings["auth"])
				require.Equal(t, "catalog", settings["database"])
				require.Contains(t, string(plan.Files["azure.yaml"]),
					"python .azd-spring/bootstrap-postgresql-managed-identity.py prepare")
				require.Contains(t, string(plan.Files["azure.yaml"]),
					"python .azd-spring/bootstrap-postgresql-managed-identity.py bootstrap")
				require.Contains(t, binding, "azure.managedIdentityEnabled=true")
				require.Contains(t, binding, "sslmode=verify-full")
				require.Contains(t, binding,
					"sslrootcert="+url.QueryEscape("/etc/ssl/certs/ca-certificates.crt"))
				require.NotContains(t, binding, "spring.datasource.hikari.")
				require.Contains(t, binding, "spring.datasource.password=\n")
				require.NotContains(t, binding, "_PASSWORD}")
				require.Contains(t, binding, "spring.sql.init.mode=never")
				require.Contains(t, string(plan.Files["azure.yaml"]), "postprovision:")
			} else {
				require.NotContains(t, binding, "authenticationPlugin")
				require.NotContains(t, string(plan.Files["azure.yaml"]), "postprovision:")
			}
			require.Contains(t, string(plan.Files["infra/main.parameters.json"]), "SERVICE_CATALOG_APP_IMAGE_NAME")
			require.Contains(t, string(plan.Files["infra/application.parameters.json"]), "SERVICE_CATALOG_APP_IMAGE_NAME")
		})
	}
}

func TestTemplateRejectsUnsupportedAuthentication(t *testing.T) {
	for _, tt := range []struct {
		name       string
		kind       ResourceKind
		auth       string
		dependency bool
		errorText  string
	}{
		{"mysql identity", MySQL, "managedIdentity", true, "MySQL managed identity is deferred"},
		{"missing token dependency", PostgreSQL, "managedIdentity", false, "azure-identity-extensions"},
		{"unknown authentication", PostgreSQL, "automatic", true, "authentication must be"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Application{
				Name: "catalog", JavaVersion: 17, Port: 8080, JarName: "catalog.jar",
				IdentityExtensions: tt.dependency,
				Requirements:       []Requirement{{Kind: tt.kind, Name: "catalog", Authentication: tt.auth}},
			}
			_, err := Generate(t.Context(), a)
			require.ErrorContains(t, err, tt.errorText)
		})
	}
}

type cancelDuringWrite struct {
	context.Context
	checks int
}

func (c *cancelDuringWrite) Err() error {
	c.checks++
	if c.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestWriteRollbackAndConfinement(t *testing.T) {
	directory := t.TempDir()
	plan := &Plan{Files: map[string][]byte{
		".azd-spring/first": []byte("first"), ".azd-spring/second": []byte("second"), "azure.yaml": []byte("project"),
	}}
	ctx := &cancelDuringWrite{Context: t.Context()}
	require.ErrorIs(t, plan.Write(ctx, directory), context.Canceled)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	plan.Files["../outside"] = []byte("must not escape")
	require.ErrorContains(t, plan.Write(t.Context(), directory), "invalid output path")
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWriteRejectsSymlink(t *testing.T) {
	directory, target := t.TempDir(), t.TempDir()
	err := os.Symlink(target, filepath.Join(directory, "infra"))
	if err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	plan := &Plan{Files: map[string][]byte{"infra/main.bicep": []byte("must not escape")}}
	require.ErrorContains(t, plan.Write(t.Context(), directory), "already exists")
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries)
}
