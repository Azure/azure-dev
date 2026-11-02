// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func springConfigFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return root
}

func TestSpringPropertiesEscapesAndDocuments(t *testing.T) {
	documents, err := parseSpringProperties("# comment\nescaped\\ key: a\\tvalue\\\n  continued\n" +
		"unicode=\\uD83D\\uDE00\nx=old\nx=new\n#---\nx=profile\nempty\n")
	require.NoError(t, err)
	require.Len(t, documents, 2)
	require.Equal(t, "a\tvaluecontinued", documents[0]["escaped key"])
	require.Equal(t, "\U0001f600", documents[0]["unicode"])
	require.Equal(t, "new", documents[0]["x"])
	require.Equal(t, "profile", documents[1]["x"])
	require.Equal(t, "", documents[1]["empty"])
	for _, value := range []string{`x=\uZZZZ`, `x=\uD83D`, `x=\uDE00`} {
		_, err := parseSpringProperties(value)
		require.Error(t, err)
		require.NotContains(t, err.Error(), value)
	}
}

func TestSpringConfigurationProfileAndSourcePrecedence(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.yaml": "spring:\n  datasource:\n    username: yaml\n",
		"src/main/resources/application.properties": "spring.profiles.active=prod\n" +
			"spring.profiles.group.prod=database\nspring.datasource.username=base\n",
		"src/main/resources/application-prod.properties":     "spring.datasource.username=prod\n",
		"src/main/resources/application-database.properties": "spring.datasource.username=database\n",
		"config/application.properties":                      "spring.datasource.username=external\n",
	})
	input := springConfigInput{root: root, environment: map[string]string{}}
	configuration, err := loadSpringConfiguration(t.Context(), input)
	require.NoError(t, err)
	value, present, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "external", value)
	require.Equal(t, []string{"prod", "database"}, configuration.profiles)
	input.environment["SPRING_DATASOURCE_USERNAME"] = "environment"
	input.jvm = []string{"-Dspring.datasource.username=system"}
	input.environment["SPRING_APPLICATION_JSON"] = `{"spring":{"datasource":{"username":"json"}}}`
	input.arguments = []string{"--spring.datasource.username=argument"}
	configuration, err = loadSpringConfiguration(t.Context(), input)
	require.NoError(t, err)
	value, _, err = configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "argument", value)
	input.arguments = nil
	configuration, err = loadSpringConfiguration(t.Context(), input)
	require.NoError(t, err)
	value, _, err = configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "json", value)
	input.environment["SPRING_APPLICATION_JSON"] = `{"spring":{"datasource":{"username":null}}}`
	configuration, err = loadSpringConfiguration(t.Context(), input)
	require.NoError(t, err)
	value, _, err = configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "system", value)
}

func TestSpringConfigurationMatchesPackagedJvmOptionSemantics(t *testing.T) {
	for _, test := range []struct {
		name        string
		arguments   []string
		jvm         []string
		environment map[string]string
		want        string
	}{
		{name: "repeated option", arguments: []string{
			"--spring.datasource.username=first", "--spring.datasource.username=second",
		}, want: "first,second"},
		{name: "valueless option before value", arguments: []string{
			"--spring.datasource.username", "--spring.datasource.username=second",
		}, want: "second"},
		{name: "explicit empty option before value", arguments: []string{
			"--spring.datasource.username=", "--spring.datasource.username=second",
		}, want: ",second"},
		{name: "valueless JVM property", jvm: []string{"-Dspring.datasource.username"},
			environment: map[string]string{"SPRING_DATASOURCE_USERNAME": "environment"}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{
				root: t.TempDir(), environment: test.environment, arguments: test.arguments, jvm: test.jvm,
			})
			require.NoError(t, err)
			value, present, err := configuration.resolve("spring.datasource.username", false)
			require.NoError(t, err)
			require.True(t, present)
			require.Equal(t, test.want, value)
		})
	}
}

func TestSpringConfigurationIndexedEnvironmentProfileGroups(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.properties":          "spring.datasource.username=base\n",
		"src/main/resources/application-database.properties": "spring.datasource.username=database\n",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{
		root: root, environment: map[string]string{
			"SPRING_PROFILES_ACTIVE_0": "prod", "SPRING_PROFILES_GROUP_PROD_0": "database",
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"prod", "database"}, configuration.profiles)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "database", value)
}

func TestSpringConfigurationRejectsAmbiguousOverrideAliases(t *testing.T) {
	for _, input := range []springConfigInput{
		{environment: map[string]string{
			"SPRING_DATASOURCE_USERNAME": "first", "spring.datasource.username": "second",
		}},
		{arguments: []string{"--spring.datasource.username=first", "--spring.datasource.user-name=second"}},
		{arguments: []string{"--spring.application.json={}", "--spring.application.json={}"}},
		{jvm: []string{"-D"}},
	} {
		input.root = t.TempDir()
		_, err := loadSpringConfiguration(t.Context(), input)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "first")
		require.NotContains(t, err.Error(), "second")
	}
}

func TestSpringConfigurationDefersSelectorsWithoutChoosingDefaultProfiles(t *testing.T) {
	for _, selector := range []string{
		"spring.profiles.active=${PROFILE}",
		"spring.profiles.active=group\nspring.profiles.group.group=${PROFILE}",
		"spring.config.import=${IMPORT}",
	} {
		root := springConfigFixture(t, map[string]string{
			"src/main/resources/application.properties": selector + "\nspring.datasource.url=jdbc:h2:mem:test",
		})
		configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root, allowPending: true})
		require.NoError(t, err)
		require.True(t, configuration.selectionPending)
		require.Empty(t, configuration.profiles)
		intent, err := inferSpringDatasource(configuration, springDependencies{h2: true}, false)
		require.NoError(t, err)
		require.True(t, intent.selectionPending)
		require.Empty(t, intent.engine)
		_, err = loadSpringConfiguration(t.Context(), springConfigInput{root: root})
		require.Error(t, err)
	}
}

func TestSpringConfigurationResolvesLocationAndProfileSelectors(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/custom.properties":      "spring.profiles.active=group\nspring.profiles.group.group=${PROFILE}",
		"src/main/resources/custom-prod.properties": "spring.datasource.username=profile",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{
		root: root, environment: map[string]string{"SPRING_CONFIG_NAME": "${NAME}", "NAME": "custom", "PROFILE": "prod"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"group", "prod"}, configuration.profiles)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "profile", value)
}

func TestSpringConfigurationDefaultWildcardDirectories(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"config/a/application.properties": "spring.datasource.username=first",
		"config/z/application.properties": "spring.datasource.username=last",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
	require.NoError(t, err)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "last", value)
	configuration, err = loadSpringConfiguration(t.Context(), springConfigInput{root: root, deployed: true})
	require.NoError(t, err)
	_, present, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.False(t, present)
}

func TestSpringConfigurationHikariKeysAndPlaceholders(t *testing.T) {
	for _, yaml := range []bool{false, true} {
		name := "application.properties"
		content := "spring.datasource.hikari.data-source-properties[azure.clientId]=${AZURE_CLIENT_ID:}\n" +
			"spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}\n"
		if yaml {
			name = "application.yaml"
			content = "spring:\n  datasource:\n    url: '${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}'\n" +
				"    hikari:\n      data-source-properties:\n        azure.clientId: '${AZURE_CLIENT_ID:}'\n"
		}
		root := springConfigFixture(t, map[string]string{"src/main/resources/" + name: content})
		configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{
			root: root, deployed: true, environment: map[string]string{
				"AZURE_CLIENT_ID": "client", "POSTGRES_URL": "jdbc:postgresql://actual.example/petclinic",
			},
		})
		require.NoError(t, err)
		client, present, err := configuration.resolve(hikariPropertiesPrefix+"[azure.clientId]", false)
		require.NoError(t, err)
		require.True(t, present)
		require.Equal(t, "client", client)
		url, _, err := configuration.resolve("spring.datasource.url", false)
		require.NoError(t, err)
		require.Equal(t, "jdbc:postgresql://actual.example/petclinic", url)
	}
}

func TestSpringConfigurationImportsAndActivatedDocuments(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.properties": "spring.profiles.active=prod\n" +
			"spring.config.import=extra.properties\nspring.datasource.username=base\n" +
			"#---\nspring.config.activate.on-profile=!prod\nspring.config.import=configserver:secret\n",
		"src/main/resources/extra.properties":      "spring.datasource.username=imported\n",
		"src/main/resources/extra-prod.properties": "spring.datasource.username=imported-profile\n",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
	require.NoError(t, err)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "imported-profile", value)
}

func TestSpringConfigurationRejectsUnsafeOrAmbiguousSources(t *testing.T) {
	for name, content := range map[string]string{
		"import cycle":      "spring.config.import=application.properties",
		"remote import":     "spring.config.import=optional:configserver:credential",
		"placeholder cycle": "spring.datasource.url=${loop}\nloop=${spring.datasource.url}",
		"profile cycle":     "spring.profiles.active=prod\nspring.profiles.group.prod=prod",
		"aliases":           "spring.datasource.username=one\nspring.datasource.user-name=two",
	} {
		t.Run(name, func(t *testing.T) {
			root := springConfigFixture(t, map[string]string{"src/main/resources/application.properties": content})
			configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
			if err == nil {
				_, _, err = configuration.resolve("spring.datasource.url", false)
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "credential")
		})
	}
}

func TestSpringConfigurationDoesNotReadHostExternalFilesForDeployedApp(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.properties": "spring.datasource.username=packaged",
		"application.properties":                    "spring.datasource.username=host",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root, deployed: true})
	require.NoError(t, err)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "packaged", value)
	_, err = loadSpringConfiguration(t.Context(), springConfigInput{
		root: root, deployed: true,
		environment: map[string]string{"SPRING_CONFIG_LOCATION": "file:./application.properties"},
	})
	require.Error(t, err)
}

func TestSpringProfilesMatchFrameworkGroupingRules(t *testing.T) {
	for _, expression := range []string{"prod", "prod & !test", "(prod & us) | eu", "cloud,prod"} {
		match, err := matchesSpringProfiles(expression, []string{"prod", "us"})
		require.NoError(t, err)
		require.True(t, match)
	}
	for _, expression := range []string{"prod & us | eu", "(prod", "prod || us", "", "prod,"} {
		_, err := matchesSpringProfiles(expression, []string{"prod"})
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := loadSpringConfiguration(ctx, springConfigInput{root: t.TempDir()})
	require.ErrorIs(t, err, context.Canceled)
}
