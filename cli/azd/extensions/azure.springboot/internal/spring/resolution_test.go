// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func resolutionFixture(t *testing.T, name string) (string, ModelResolver) {
	t.Helper()
	directory := t.TempDir()
	source := os.DirFS(filepath.Join("testdata", "resolution", name))
	require.NoError(t, fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		writeFixture(t, directory, name, string(data))
		return nil
	}))
	writeFixture(t, directory, "pom.xml", "<project/>")
	return directory, staticResolver{model: effectiveModel(t, directory, allDrivers())}
}

func TestResolutionFixtures(t *testing.T) {
	for _, tt := range []struct {
		fixture    string
		profiles   []string
		kind       ResourceKind
		decision   DecisionKind
		diagnostic string
	}{
		{fixture: "h2-mysql", kind: "h2"},
		{fixture: "h2-postgres", kind: "h2"},
		{fixture: "three-engines", kind: "h2"},
		{fixture: "three-engines", profiles: []string{"mysql"}, kind: MySQL},
		{fixture: "three-engines", profiles: []string{"postgres"}, kind: PostgreSQL},
		{fixture: "default-postgres", kind: PostgreSQL},
		{fixture: "profile-choice", decision: Ambiguous},
		{fixture: "profile-choice", profiles: []string{"postgres"}, kind: PostgreSQL},
		{fixture: "default-profile-choice", decision: Ambiguous},
		{fixture: "default-profile-choice", profiles: []string{"h2"}, kind: "h2"},
		{fixture: "missing-url", decision: MissingInput},
		{fixture: "missing-port", decision: MissingInput},
		{fixture: "prompt-driver", decision: MissingInput},
		{fixture: "driver-conflict", diagnostic: "invalid"},
		{fixture: "file-storage", diagnostic: "unsupported"},
	} {
		t.Run(tt.fixture+strings.Join(tt.profiles, ","), func(t *testing.T) {
			directory, resolver := resolutionFixture(t, tt.fixture)
			result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: tt.profiles})
			require.NoError(t, err)
			if tt.decision != "" {
				require.Empty(t, result.Diagnostics)
				require.Len(t, result.Decisions, 1)
				decision := result.Decisions[0]
				require.Equal(t, tt.decision, decision.Kind)
				require.Equal(t, []string{"app"}, decision.ApplicationIDs)
				require.NotEmpty(t, decision.Evidence)
				if tt.fixture == "profile-choice" || tt.fixture == "default-profile-choice" {
					if tt.fixture == "profile-choice" {
						require.Equal(t, []string{"h2", "mysql", "postgres"}, decision.Choices)
					} else {
						require.Equal(t, []string{"h2", "postgres"}, decision.Choices)
					}

					require.True(t, decision.Profiles)
					require.Empty(t, decision.BindingIDs)
				} else if tt.fixture == "missing-url" || tt.fixture == "prompt-driver" {
					require.Empty(t, decision.Choices)
					require.Equal(t, []string{"app/datasource:primary"}, decision.BindingIDs)
					require.Len(t, result.Bindings, 1)
					require.Equal(t, result.Bindings[0].ID, decision.BindingIDs[0])
					require.Empty(t, result.Resources, "missing URLs must not guess resource kinds from drivers")
				} else {
					require.Empty(t, decision.BindingIDs)
				}
				_, err = result.ResolvedApplication()
				require.ErrorContains(t, err, "unresolved")
				return
			}
			require.Empty(t, result.Decisions)
			if tt.diagnostic != "" {
				require.Len(t, result.Diagnostics, 1)
				require.Equal(t, tt.diagnostic, result.Diagnostics[0].Kind)
				_, err = result.ResolvedApplication()
				require.ErrorContains(t, err, "blocking")
				return
			}
			require.Empty(t, result.Diagnostics)
			require.Len(t, result.Resources, 1)
			require.Len(t, result.Bindings, 1)
			require.Equal(t, tt.kind, result.Resources[0].Kind)
			require.Equal(t, result.Resources[0].ID, result.Bindings[0].ResourceID)
			require.NotEmpty(t, result.Bindings[0].Evidence)
			application, err := result.ResolvedApplication()
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			properties := string(plan.Files[".azd-spring/application-azd.properties"])
			if tt.kind == "h2" {
				require.Equal(t, ApplicationLocal, result.Resources[0].Disposition)
				require.Contains(t, properties, "spring.datasource.url=jdbc:h2:mem:catalog\n")
				require.Empty(t, application.Requirements)
			} else {
				require.Equal(t, Provisioned, result.Resources[0].Disposition)
				require.Len(t, application.Requirements, 1)
			}
		})
	}
}

func TestApplicationInputs(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		ApplicationInputs: map[string]ApplicationInputKind{
			"FRONTEND_URL": ApplicationInputValue,
			"JWT_SECRET":   ApplicationInputSecret,
		},
	})
	require.NoError(t, err)
	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	require.Equal(t, []ApplicationInput{
		{Name: "FRONTEND_URL", Kind: ApplicationInputValue},
		{Name: "JWT_SECRET", Kind: ApplicationInputSecret},
	}, application.ApplicationInputs)
}

func TestApplicationInputValidation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		inputs    map[string]ApplicationInputKind
		errorText string
	}{
		{
			name:      "invalid name",
			inputs:    map[string]ApplicationInputKind{"JWT-SECRET": ApplicationInputSecret},
			errorText: "valid environment variable name",
		},
		{
			name: "case conflict",
			inputs: map[string]ApplicationInputKind{
				"JWT_SECRET": ApplicationInputSecret,
				"jwt_secret": ApplicationInputSecret,
			},
			errorText: "differ only by case",
		},
		{
			name:      "deployment binding conflict",
			inputs:    map[string]ApplicationInputKind{"spring_datasource_url": ApplicationInputValue},
			errorText: "deployment-owned binding",
		},
		{
			name:      "invalid classification",
			inputs:    map[string]ApplicationInputKind{"JWT_SECRET": "generated"},
			errorText: "value or secret",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory, resolver := resolutionFixture(t, "three-engines")
			_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
				ApplicationInputs: tt.inputs,
			})
			require.ErrorContains(t, err, tt.errorText)
		})
	}
}

func TestApplicationInputReservedConfigurationSources(t *testing.T) {
	names := []string{
		"SPRING_APPLICATION_JSON",
		"JAVA_TOOL_OPTIONS",
		"JDK_JAVA_OPTIONS",
		"_JAVA_OPTIONS",
	}
	for _, setting := range deploymentOwnedHostingSettings {
		names = append(names, environmentPropertyNames(setting)[1:]...)
	}
	for _, setting := range deploymentOwnedConfigurationSettings {
		names = append(names, environmentPropertyNames(setting)[1:]...)
	}
	for _, setting := range deploymentOwnedRuntimeSettings {
		names = append(names, environmentPropertyNames(setting)[1:]...)
	}
	for _, setting := range unsupportedDatasourceSettings {
		names = append(names, environmentPropertyNames(setting.key)[1:]...)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(name+"/"+string(kind), func(t *testing.T) {
				_, err := resolveApplicationInputs(map[string]ApplicationInputKind{name: kind})
				require.ErrorContains(t, err, "conflicts with a deployment-owned binding")
			})
		}
	}
}

func TestApplicationInputReservedIndexedConfigurationSources(t *testing.T) {
	for _, name := range []string{
		"SPRING_AUTOCONFIGURE_EXCLUDE_12",
		"SPRING_CONFIG_IMPORT_0",
		"SPRING_PROFILES_ACTIVE_0",
		"SPRING_PROFILES_INCLUDE_0",
		"SPRING_PROFILES_INCLUDE_0_",
	} {
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(name+"/"+string(kind), func(t *testing.T) {
				_, err := resolveApplicationInputs(map[string]ApplicationInputKind{name: kind})
				require.ErrorContains(t, err, "conflicts with a deployment-owned binding")
			})
		}
	}
}

func TestApplicationInputPlaceholderConflicts(t *testing.T) {
	for _, tt := range []struct {
		name          string
		configuration string
		input         string
		property      string
	}{
		{
			name:          "direct web application type",
			configuration: "spring.main.web-application-type=${APP_MODE:servlet}\n",
			input:         "APP_MODE",
			property:      "spring.main.web-application-type",
		},
		{
			name: "indirect web application type",
			configuration: "spring.main.web-application-type=${WEB_MODE:servlet}\n" +
				"WEB_MODE=${APP_MODE:servlet}\n",
			input:    "APP_MODE",
			property: "spring.main.web-application-type",
		},
		{
			name:          "direct server SSL",
			configuration: "server.ssl.enabled=${ENABLE_SSL:false}\n",
			input:         "ENABLE_SSL",
			property:      "server.ssl.enabled",
		},
		{
			name: "indirect server SSL",
			configuration: "server.ssl.enabled=${SSL_MODE:false}\n" +
				"SSL_MODE=${ENABLE_SSL:false}\n",
			input:    "ENABLE_SSL",
			property: "server.ssl.enabled",
		},
		{
			name:          "direct profile include",
			configuration: "spring.profiles.include=${EXTRA_PROFILES:}\n",
			input:         "EXTRA_PROFILES",
			property:      "spring.profiles.include",
		},
		{
			name: "indirect profile include",
			configuration: "spring.profiles.include=${INCLUDE_ALIAS:}\n" +
				"INCLUDE_ALIAS=${EXTRA_PROFILES:}\n",
			input:    "EXTRA_PROFILES",
			property: "spring.profiles.include",
		},
		{
			name:          "nested placeholder outer name",
			configuration: "spring.main.web-application-type=${APP_MODE:${DEFAULT_MODE:servlet}}\n",
			input:         "APP_MODE",
			property:      "spring.main.web-application-type",
		},
		{
			name:          "nested placeholder fallback dependency",
			configuration: "spring.main.web-application-type=${APP_MODE:${DEFAULT_MODE:servlet}}\n",
			input:         "DEFAULT_MODE",
			property:      "spring.main.web-application-type",
		},
		{
			name:          "dotted placeholder environment alias",
			configuration: "spring.main.web-application-type=${app.mode:servlet}\n",
			input:         "APP_MODE",
			property:      "spring.main.web-application-type",
		},
		{
			name: "indirect dotted placeholder environment alias",
			configuration: "spring.main.web-application-type=${web.mode:servlet}\n" +
				"web.mode=${app.mode:servlet}\n",
			input:    "APP_MODE",
			property: "spring.main.web-application-type",
		},
		{
			name: "interpolated placeholder name",
			configuration: "MODE_KEY=app.mode\n" +
				"spring.main.web-application-type=${${MODE_KEY}:servlet}\n",
			input:    "APP_MODE",
			property: "spring.main.web-application-type",
		},
		{
			name:          "datasource JNDI selector",
			configuration: "spring.datasource.jndi-name=${APP_JNDI:}\n",
			input:         "APP_JNDI",
			property:      "spring.datasource.jndi-name",
		},
		{
			name:          "datasource passwordless selector",
			configuration: "spring.datasource.azure.passwordless-enabled=${PASSWORDLESS:false}\n",
			input:         "PASSWORDLESS",
			property:      "spring.datasource.azure.passwordless-enabled",
		},
		{
			name:          "cloud JDBC passwordless selector",
			configuration: "spring.cloud.azure.jdbc.passwordless-enabled=${PASSWORDLESS:false}\n",
			input:         "PASSWORDLESS",
			property:      "spring.cloud.azure.jdbc.passwordless-enabled",
		},
		{
			name:          "datasource auto-configuration selector",
			configuration: "spring.autoconfigure.exclude=${AUTO_CONFIG_EXCLUDES:}\n",
			input:         "AUTO_CONFIG_EXCLUDES",
			property:      "spring.autoconfigure.exclude",
		},
		{
			name:          "embedded database selector",
			configuration: "spring.datasource.embedded-database-connection=${DB_CONNECTION:}\n",
			input:         "DB_CONNECTION",
			property:      "spring.datasource.embedded-database-connection",
		},
	} {
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(tt.name+"/"+string(kind), func(t *testing.T) {
				directory, resolver := resolutionFixture(t, "three-engines")
				writeFixture(t, directory, "src/main/resources/application.properties",
					"spring.datasource.url=jdbc:h2:mem:catalog\n"+tt.configuration)
				_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
					ApplicationInputs: map[string]ApplicationInputKind{tt.input: kind},
				})
				require.ErrorContains(t, err, "application input "+tt.input+
					" affects deployment-owned property "+tt.property)
			})
		}
	}
}

func TestApplicationInputProfileGroupConflicts(t *testing.T) {
	for _, tt := range []struct {
		name          string
		configuration string
		profiles      []string
		input         string
	}{
		{
			name: "profile group placeholder",
			configuration: "spring.profiles.active=release\n" +
				"spring.profiles.group.release=${EXTRA_PROFILES:}\n",
			input: "EXTRA_PROFILES",
		},
		{
			name: "profile group environment binding",
			configuration: "spring.profiles.active=release\n" +
				"spring.profiles.group.release=worker\n",
			input: "SPRING_PROFILES_GROUP_RELEASE",
		},
		{
			name:     "selected profile group environment binding",
			profiles: []string{"release"},
			input:    "SPRING_PROFILES_GROUP_RELEASE",
		},
		{
			name:     "selected profile group indexed environment binding",
			profiles: []string{"release"},
			input:    "SPRING_PROFILES_GROUP_RELEASE_0",
		},
		{
			name:     "selected profile group indexed trailing delimiter",
			profiles: []string{"release"},
			input:    "SPRING_PROFILES_GROUP_RELEASE_0_",
		},
	} {
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(tt.name+"/"+string(kind), func(t *testing.T) {
				directory, resolver := resolutionFixture(t, "three-engines")
				writeFixture(t, directory, "src/main/resources/application.properties",
					"spring.datasource.url=jdbc:h2:mem:catalog\n"+tt.configuration)
				_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
					Profiles: tt.profiles,
					ApplicationInputs: map[string]ApplicationInputKind{
						tt.input: kind,
					},
				})
				require.ErrorContains(t, err, "application input "+tt.input+
					" affects deployment-owned property spring.profiles.group.release")
			})
		}
	}
}

func TestApplicationInputProfileIncludeSourceConflicts(t *testing.T) {
	for _, tt := range []struct {
		name  string
		files map[string]string
	}{
		{
			name: "packaged include overridden externally",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.include=${EXTRA_PROFILES:}\n",
				"config/application.properties":             "spring.profiles.include=\n",
			},
		},
		{
			name: "earlier YAML document include",
			files: map[string]string{
				"src/main/resources/application.yaml": "spring:\n  profiles:\n    include: ${EXTRA_PROFILES:}\n---\n" +
					"spring:\n  profiles:\n    include:\n",
			},
		},
		{
			name: "include alias overridden by selected profile",
			files: map[string]string{
				"src/main/resources/application.properties": "spring.profiles.active=release\n" +
					"spring.profiles.include=${INCLUDE_ALIAS:}\n" +
					"INCLUDE_ALIAS=${EXTRA_PROFILES:}\n",
				"src/main/resources/application-release.properties": "INCLUDE_ALIAS=\n",
			},
		},
	} {
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(tt.name+"/"+string(kind), func(t *testing.T) {
				directory, resolver := resolutionFixture(t, "three-engines")
				for file, content := range tt.files {
					writeFixture(t, directory, file, content)
				}
				_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
					ApplicationInputs: map[string]ApplicationInputKind{
						"EXTRA_PROFILES": kind,
					},
				})
				require.ErrorContains(t, err, "application input EXTRA_PROFILES"+
					" affects deployment-owned property spring.profiles.include")
			})
		}
	}
}

func TestApplicationInputFoundrySelectorPlaceholderConflicts(t *testing.T) {
	for _, tt := range []struct {
		property string
		input    string
		extra    string
	}{
		{
			property: "spring.ai.model.chat",
			input:    "CHAT_MODEL",
			extra:    "spring.ai.azure.openai.chat.options.deployment-name=chat\n",
		},
		{
			property: "spring.ai.model.embedding",
			input:    "EMBEDDING_MODEL",
			extra:    "spring.ai.azure.openai.embedding.options.deployment-name=embeddings\n",
		},
	} {
		for _, kind := range []ApplicationInputKind{ApplicationInputValue, ApplicationInputSecret} {
			t.Run(tt.property+"/"+string(kind), func(t *testing.T) {
				directory := t.TempDir()
				writeFixture(t, directory, "pom.xml", "<project/>")
				writeFixture(t, directory, "src/main/resources/application.properties",
					tt.property+"=${"+tt.input+":azure-openai}\n"+tt.extra)
				resolver := staticResolver{model: effectiveModel(t, directory,
					dependency("org.springframework.ai", "spring-ai-starter-model-azure-openai", "compile"))}
				_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
					ApplicationInputs: map[string]ApplicationInputKind{
						tt.input: kind,
					},
				})
				require.ErrorContains(t, err, "application input "+tt.input+
					" affects deployment-owned property "+tt.property)
			})
		}
	}
}

func TestResolutionIgnoresAmbientConfiguration(t *testing.T) {
	t.Setenv("SPRING_PROFILES_ACTIVE", "postgres")
	t.Setenv("SPRING_DATASOURCE_URL", "jdbc:postgresql://ignored/not_selected")
	t.Setenv("SPRING_APPLICATION_JSON", `{"server":{"port":1234}}`)
	t.Setenv("DEPLOYMENT_JDBC_URL", "jdbc:mysql://ignored/not_selected")
	directory, resolver := resolutionFixture(t, "three-engines")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Equal(t, ResourceKind("h2"), result.Resources[0].Kind)
	require.Equal(t, 8080, result.Applications[0].Port)
	directory, resolver = resolutionFixture(t, "missing-url")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Decisions, 1)
}

func TestResolutionFoundryBinding(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}\n"+
			"spring.ai.azure.openai.api-key=${AZURE_OPENAI_API_KEY}\n"+
			"spring.ai.azure.openai.chat.options.deployment-name=${AZURE_OPENAI_DEPLOYMENT_NAME}\n")
	resolver := staticResolver{model: effectiveModel(t, directory,
		dependency("org.springframework.ai", "spring-ai-starter-model-azure-openai", "compile"))}

	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Empty(t, result.Decisions)
	require.Empty(t, result.Diagnostics)
	require.Equal(t, []Resource{{
		ID: "foundry:primary", Kind: Foundry, Name: "azure-openai", Disposition: Provisioned,
	}}, result.Resources)
	require.Len(t, result.Bindings, 1)
	require.Equal(t, "app/foundry:primary", result.Bindings[0].ID)
	require.Equal(t, "spring.ai.azure.openai", result.Bindings[0].PropertyPrefix)
	require.Equal(t, "apiKey", result.Bindings[0].Authentication)
	require.NotNil(t, result.Bindings[0].Foundry)
	require.NotEmpty(t, result.Bindings[0].Evidence)

	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	require.NotNil(t, application.Foundry)
	require.Equal(t, "gpt-5.6-sol", application.Foundry.ChatDeploymentName)
	require.Equal(t, "apiKey", application.Foundry.Authentication)
}

func TestResolutionFoundryEmbeddingBinding(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.ai.model.embedding=azure-openai\n"+
			"spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}\n"+
			"spring.ai.azure.openai.embedding.options.deployment-name="+
			"${AZURE_OPENAI_EMBEDDING_DEPLOYMENT_NAME}\n")
	resolver := staticResolver{model: effectiveModel(t, directory,
		dependency("org.springframework.ai", "spring-ai-starter-model-azure-openai", "compile"))}

	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Resources, 1)
	require.Len(t, result.Bindings, 1)
	require.NotNil(t, result.Bindings[0].Foundry)
	require.Equal(t, "text-embedding-3-small", result.Bindings[0].Foundry.EmbeddingDeploymentName)

	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	require.NotNil(t, application.Foundry)
	require.Empty(t, application.Foundry.ChatDeploymentName)
	require.Equal(t, "text-embedding-3-small", application.Foundry.EmbeddingDeploymentName)
}

func TestResolutionFoundryManagedIdentityBinding(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}\n"+
			"spring.ai.azure.openai.chat.options.deployment-name=${AZURE_OPENAI_DEPLOYMENT_NAME}\n")
	resolver := staticResolver{model: effectiveModel(t, directory,
		dependency("org.springframework.ai", "spring-ai-starter-model-azure-openai", "compile"))}

	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Bindings, 1)
	require.Equal(t, "managedIdentity", result.Bindings[0].Authentication)

	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	require.NotNil(t, application.Foundry)
	require.Equal(t, "managedIdentity", application.Foundry.Authentication)
}

func TestResolutionSupportsDatasourceAndFoundry(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "pom.xml", "<project/>")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:postgresql://localhost/catalog\n"+
			"spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}\n"+
			"spring.ai.azure.openai.api-key=${AZURE_OPENAI_API_KEY}\n")
	resolver := staticResolver{model: effectiveModel(t, directory,
		dependency("org.postgresql", "postgresql", "runtime")+
			dependency("org.springframework.ai", "spring-ai-starter-model-azure-openai", "compile"))}

	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Resources, 2)
	require.Len(t, result.Bindings, 2)
	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	require.Len(t, application.Requirements, 1)
	require.NotNil(t, application.Foundry)
}

func TestResolutionExternalDrivers(t *testing.T) {
	for _, tt := range []struct {
		name, scheme, driver, extra, want, diagnostic string
		inputs                                        map[string]string
	}{
		{name: "automatic PostgreSQL driver", scheme: "postgresql"},
		{name: "automatic MySQL driver", scheme: "mysql"},
		{name: "explicit PostgreSQL driver", scheme: "postgresql",
			driver: "org.postgresql.Driver", want: "org.postgresql.Driver"},
		{name: "explicit MySQL driver", scheme: "mysql",
			driver: "com.mysql.cj.jdbc.Driver", want: "com.mysql.cj.jdbc.Driver"},
		{name: "legacy MySQL driver", scheme: "mysql",
			driver: "com.mysql.jdbc.Driver", want: "com.mysql.jdbc.Driver"},
		{name: "project driver property", scheme: "postgresql", driver: "${app.driver}",
			extra: "app.driver=org.postgresql.Driver\n", want: "org.postgresql.Driver"},
		{name: "driver placeholder default", scheme: "postgresql",
			driver: "${JDBC_DRIVER:org.postgresql.Driver}", want: "org.postgresql.Driver"},
		{name: "shared prompt input", scheme: "${DB_ENGINE}", driver: "org.${DB_ENGINE}.Driver",
			inputs: map[string]string{"DB_ENGINE": "postgresql"}, want: "org.postgresql.Driver"},
		{name: "conflicting driver", scheme: "postgresql", driver: "com.mysql.cj.jdbc.Driver", diagnostic: "invalid"},
		{name: "custom driver", scheme: "postgresql", driver: "example.CustomDriver", diagnostic: "invalid"},
		{name: "missing driver input is not a new prompt", scheme: "postgresql",
			driver: "${JDBC_DRIVER}", diagnostic: "unsupported"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory, resolver := resolutionFixture(t, "three-engines")
			properties := "spring.datasource.url=jdbc:" + tt.scheme + "://localhost/catalog\n" + tt.extra
			if tt.driver != "" {
				properties += "spring.datasource.driver-class-name=" + tt.driver + "\n"
			}
			writeFixture(t, directory, "src/main/resources/application.properties", properties)
			analysis, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Properties: tt.inputs})
			require.NoError(t, err)
			require.Empty(t, analysis.Decisions)
			if tt.diagnostic != "" {
				require.Len(t, analysis.Diagnostics, 1)
				require.Equal(t, tt.diagnostic, analysis.Diagnostics[0].Kind)
				_, err = analysis.ResolvedApplication()
				require.Error(t, err)
				return
			}
			require.Empty(t, analysis.Diagnostics)
			require.Len(t, analysis.Bindings, 1)
			require.Equal(t, tt.want, analysis.Bindings[0].Driver)
			if tt.want != "" {
				require.Contains(t, analysis.Bindings[0].Evidence, Evidence{
					Property: "spring.datasource.driver-class-name", Source: "src/main/resources/application.properties",
				})
			}
			application, err := analysis.ResolvedApplication()
			require.NoError(t, err)
			require.Len(t, application.Requirements, 1)
			require.Equal(t, tt.want, application.Requirements[0].Driver)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			generated := string(plan.Files[".azd-spring/application-azd.properties"])
			if tt.want == "" {
				require.NotContains(t, generated, "spring.datasource.driver-class-name=")
			} else {
				require.Contains(t, generated, "spring.datasource.driver-class-name="+tt.want+"\n")
			}
			require.NotContains(t, generated, "${DB_ENGINE}")
			require.NotContains(t, generated, "${JDBC_DRIVER}")
		})
	}
}

func TestResolutionEmbeddedDriverSharedPromptInput(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:${DB_ENGINE}:mem:catalog\n"+
			"spring.datasource.driver-class-name=org.${DB_ENGINE}.Driver\n")
	analysis, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"DB_ENGINE": "h2"},
	})
	require.NoError(t, err)
	require.Empty(t, analysis.Diagnostics)
	require.Empty(t, analysis.Decisions)
	require.Len(t, analysis.Bindings, 1)
	require.NotNil(t, analysis.Bindings[0].Embedded)
	require.Equal(t, "org.h2.Driver", analysis.Bindings[0].Embedded.Driver)
	require.Contains(t, analysis.Bindings[0].Evidence, Evidence{
		Property: "spring.datasource.driver-class-name", Source: "src/main/resources/application.properties",
	})

	application, err := analysis.ResolvedApplication()
	require.NoError(t, err)
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	generated := string(plan.Files[".azd-spring/application-azd.properties"])
	require.Contains(t, generated, "spring.datasource.url=jdbc:h2:mem:catalog\n")
	require.Contains(t, generated, "spring.datasource.driver-class-name=org.h2.Driver\n")
	require.NotContains(t, generated, "${DB_ENGINE}")
}

func TestResolutionAnswersAndProvenance(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	address := url.URL{
		Scheme: "postgresql", Host: "localhost", Path: "/catalog",
		User: url.UserPassword("test-user", "test-password"),
	}
	inputs := DeploymentInputs{Properties: map[string]string{
		"DEPLOYMENT_JDBC_URL": "jdbc:" + address.String(),
	}}
	result, err := AnalyzeProject(t.Context(), directory, resolver, inputs)
	require.NoError(t, err)
	require.Empty(t, result.Decisions)
	require.Equal(t, PostgreSQL, result.Resources[0].Kind)
	require.Equal(t, []Evidence{
		{Property: "spring.datasource.url", Source: "src/main/resources/application.properties"},
		{Property: "DEPLOYMENT_JDBC_URL", Source: "deployment input"},
	}, result.Bindings[0].Evidence)
	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	for _, sensitive := range []string{"localhost", "test-user", "test-password"} {
		require.NotContains(t, string(serialized), sensitive)
	}

	directory, resolver = resolutionFixture(t, "missing-port")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"HTTP_PORT": "9090"},
	})
	require.NoError(t, err)
	require.Equal(t, 9090, result.Applications[0].Port)
}

func TestResolutionRejectsSharedPromptInputOutsideGeneratedBindings(t *testing.T) {
	for _, tt := range []struct {
		name       string
		properties string
	}{
		{
			name: "direct reference",
			properties: "server.port=${HTTP_PORT}\n" +
				"management.server.port=${HTTP_PORT}\n",
		},
		{
			name: "transitive reference",
			properties: "server.port=${APP_PORT}\n" +
				"management.server.port=${APP_PORT}\n" +
				"APP_PORT=${HTTP_PORT}\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			directory, resolver := resolutionFixture(t, "three-engines")
			writeFixture(t, directory, "src/main/resources/application.properties", tt.properties)
			_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
				Properties: map[string]string{"HTTP_PORT": "9090"},
			})
			require.ErrorContains(t, err, "management.server.port")
			require.ErrorContains(t, err, "src/main/resources/application.properties")
			require.NotContains(t, err.Error(), "9090")
		})
	}
}

func TestResolutionAllowsPromptInputAliasChain(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"server.port=${APP_PORT}\nAPP_PORT=${HTTP_PORT}\n")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"HTTP_PORT": "9090"},
	})
	require.NoError(t, err)
	require.Equal(t, 9090, result.Applications[0].Port)
}

func TestResolutionRejectsPromptInputSharedWithProfileInclude(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.profiles.include=${DB_NAME:local}\n"+
			"spring.datasource.url=jdbc:postgresql://localhost/${DB_NAME}\n")
	_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"DB_NAME": "catalog"},
	})
	require.ErrorContains(t, err, "deployment input DB_NAME also affects unsupported property spring.profiles.include")
}

func TestResolutionRejectsUnusedOrUnreproducibleInputs(t *testing.T) {
	for _, properties := range []map[string]string{
		{"unused": "value"},
		{"spring.datasource.password": "do-not-copy"},
		{"spring.datasource.driver-class-name": "org.h2.Driver"},
		{"spring.profiles.active": "postgres"},
		{"server.port": "8080", "SERVER_PORT": "9090"},
	} {
		directory, resolver := resolutionFixture(t, "three-engines")
		_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Properties: properties})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "do-not-copy")
	}
}

func TestResolutionRejectsInvalidExplicitProfiles(t *testing.T) {
	for _, profiles := range [][]string{{}, {""}, {"postgres", ""}, {"not a profile"}} {
		directory, resolver := resolutionFixture(t, "three-engines")
		_, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: profiles})
		require.ErrorContains(t, err, "--profiles")
	}
}

func TestResolutionPreservesOperationalErrors(t *testing.T) {
	directory, _ := resolutionFixture(t, "three-engines")
	failure := errors.New("build unavailable")
	result, err := AnalyzeProject(t.Context(), directory, staticResolver{err: failure}, DeploymentInputs{})
	require.ErrorIs(t, err, failure)
	require.Nil(t, result)
}

func TestResolutionPlaceholderChain(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=${DATABASE_URL}\nDATABASE_URL=${CONNECTION}")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Len(t, result.Decisions, 1)
	require.Equal(t, "CONNECTION", result.Decisions[0].Input)
	require.Equal(t, []Evidence{
		{Property: "spring.datasource.url", Source: "src/main/resources/application.properties"},
		{Property: "DATABASE_URL", Source: "src/main/resources/application.properties"},
	}, result.Decisions[0].Evidence)
}

func TestResolutionValidatesReferences(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	result.Bindings[0].ResourceID = "missing"
	_, err = result.ResolvedApplication()
	require.Error(t, err)
}

func TestEmbeddedRuntimeBindings(t *testing.T) {
	for _, tt := range []struct {
		jdbc      string
		errorText string
	}{
		{"jdbc:h2:mem:catalog;DB_CLOSE_DELAY=-1;DB_CLOSE_ON_EXIT=FALSE", ""},
		{"jdbc:derby:memory:catalog;create=true", ""},
		{"jdbc:hsqldb:mem:catalog", ""},
		{"jdbc:h2:mem:catalog;PASSWORD=do-not-copy", "unsupported"},
		{"jdbc:h2:mem:catalog;INIT=do-not-copy", "unsupported"},
		{"jdbc:h2:mem:catalog;DB_CLOSE_DELAY=-1;DB_CLOSE_DELAY=0", "duplicate"},
		{"jdbc:h2:mem:catalog\ninjected=value", "unsupported"},
	} {
		t.Run(tt.jdbc, func(t *testing.T) {
			directory, resolver := resolutionFixture(t, "three-engines")
			resolver = staticResolver{model: effectiveModel(t, directory, allDrivers()+
				dependency("org.apache.derby", "derby", "runtime")+dependency("org.hsqldb", "hsqldb", "runtime"))}
			result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
				Properties: map[string]string{"spring.datasource.url": tt.jdbc},
			})
			require.NoError(t, err)
			if tt.errorText != "" {
				require.Len(t, result.Diagnostics, 1)
				require.Contains(t, result.Diagnostics[0].Message, tt.errorText)
				require.NotContains(t, result.Diagnostics[0].Message, "do-not-copy")
				return
			}
			application, err := result.ResolvedApplication()
			require.NoError(t, err)
			plan, err := Generate(t.Context(), application)
			require.NoError(t, err)
			require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]),
				"spring.datasource.url="+tt.jdbc+"\n")
			require.NotContains(t, plan.Files, ".azd-spring/prepare-database-password.ps1")
		})
	}
}

func TestResolutionRejectsUnreproducibleCredentialPlaceholder(t *testing.T) {
	directory, resolver := resolutionFixture(t, "three-engines")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.datasource.url=jdbc:h2:mem:catalog\nspring.datasource.password=${LOCAL_PASSWORD}")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Empty(t, result.Decisions, "do not request a secret that generation cannot safely reproduce")
	require.Len(t, result.Diagnostics, 1)
	require.Equal(t, "unsupported", result.Diagnostics[0].Kind)
}

func TestResolutionMaterializesExplicitEmptyDatasource(t *testing.T) {
	directory, resolver := resolutionFixture(t, "missing-url")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{
		Properties: map[string]string{"DEPLOYMENT_JDBC_URL": ""},
	})
	require.NoError(t, err)
	application, err := result.ResolvedApplication()
	require.NoError(t, err)
	plan, err := Generate(t.Context(), application)
	require.NoError(t, err)
	require.Contains(t, string(plan.Files[".azd-spring/application-azd.properties"]), "spring.datasource.url=\n")
}

func TestProfileChoicePreservesGroupNames(t *testing.T) {
	directory, resolver := resolutionFixture(t, "profile-choice")
	writeFixture(t, directory, "src/main/resources/application.properties",
		"spring.profiles.active=${DEPLOYMENT_PROFILE}\n"+
			"spring.profiles.group.Production-SQL=postgres\n"+
			"spring.profiles.group.production_sql=mysql")
	result, err := AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{})
	require.NoError(t, err)
	require.Contains(t, result.Decisions[0].Choices, "Production-SQL")
	require.Contains(t, result.Decisions[0].Choices, "production_sql")
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: []string{"Production-SQL"}})
	require.NoError(t, err)
	require.Equal(t, PostgreSQL, result.Resources[0].Kind)
	result, err = AnalyzeProject(t.Context(), directory, resolver, DeploymentInputs{Profiles: []string{"production_sql"}})
	require.NoError(t, err)
	require.Equal(t, MySQL, result.Resources[0].Kind)
}
