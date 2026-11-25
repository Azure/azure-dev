// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package springmodel

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
			"spring.config.import=extra.properties,comma.yml\nspring.datasource.username=base\n" +
			"#---\nspring.config.activate.on-profile=!prod\nspring.config.import=configserver:secret\n",
		"src/main/resources/extra.properties":      "spring.datasource.username=imported\n",
		"src/main/resources/extra-prod.properties": "spring.datasource.username=imported-profile\n",
		"src/main/resources/comma.yml": "spring:\n  config:\n    activate:\n      on-profile: staging,prod\n" +
			"custom:\n  activated: comma-or\n---\n" +
			"spring:\n  config:\n    activate:\n      on-profile:\n        - dev\n        - prod\n" +
			"custom:\n  list-activated: yaml-list-or\n",
	})
	configuration, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
	require.NoError(t, err)
	value, _, err := configuration.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.Equal(t, "imported-profile", value)
	commaVal, _, err := configuration.resolve("custom.activated", false)
	require.NoError(t, err)
	require.Equal(t, "comma-or", commaVal)
	listVal, _, err := configuration.resolve("custom.list-activated", false)
	require.NoError(t, err)
	require.Equal(t, "yaml-list-or", listVal)
}

func TestSpringConfigurationRejectsUnsafeOrAmbiguousSources(t *testing.T) {
	for name, content := range map[string]string{
		"import cycle":       "spring.config.import=application.properties",
		"bad configserver":   "spring.config.import=optional:configserver:credential",
		"unsupported scheme": "spring.config.import=optional:vault:credential",
		"placeholder cycle":  "spring.datasource.url=${loop}\nloop=${spring.datasource.url}",
		"profile cycle":      "spring.profiles.active=prod\nspring.profiles.group.prod=prod",
		"aliases":            "spring.datasource.username=one\nspring.datasource.user-name=two",
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

func TestSpringConfigServerImportHonorsOverridePrecedenceAndDisableFlag(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.yml": "spring:\n  application:\n    name: customers-service\n" +
			"  config:\n    import: optional:configserver:${CONFIG_SERVER_URL:http://localhost:8888/}\n",
	})
	postgresDeps := springDependencies{database: true, postgresql: true, azureIdentity: true}
	jdbcURL := "jdbc:postgresql://pg.example:5432/customers?" +
		"sslmode=verify-full&sslrootcert=/etc/ssl/certs/ca-certificates.crt&" +
		"authenticationPluginClassName=" + azurePostgreSQLPlugin + "&azure.managedIdentityEnabled=true"

	// 1. Database-free service with configserver import stays DatabaseNone.
	cfgFree, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root, deployed: true})
	require.NoError(t, err)
	freeDS, err := inferSpringDatasource(cfgFree, springDependencies{}, true)
	require.NoError(t, err)
	require.Equal(t, DatabaseNone, freeDS.engine)

	// 2. Pre-provision / Package (requireFinal=false) defers cleanly when URL is not yet overridden.
	cfgPending, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root, allowPending: true})
	require.NoError(t, err)
	pendingDS, err := inferSpringDatasource(cfgPending, postgresDeps, false)
	require.NoError(t, err)
	require.True(t, pendingDS.pending)
	require.True(t, pendingDS.selectionPending)

	// 3. Final verification fails closed without explicit container datasource overrides.
	_, err = inferSpringDatasource(cfgFree, postgresDeps, true)
	require.ErrorContains(t, err, "Config Server imports require explicit container datasource overrides")

	// 4. Final verification succeeds when Container App env supplies SPRING_DATASOURCE_URL + USERNAME.
	cfgOverridden, err := loadSpringConfiguration(t.Context(), springConfigInput{
		root:     root,
		deployed: true,
		environment: map[string]string{
			"SPRING_DATASOURCE_URL":      jdbcURL,
			"SPRING_DATASOURCE_USERNAME": "customers_app",
		},
	})
	require.NoError(t, err)
	finalDS, err := inferSpringDatasource(cfgOverridden, postgresDeps, true)
	require.NoError(t, err)
	require.Equal(t, DatabasePostgreSQL, finalDS.engine)
	require.Equal(t, "customers", finalDS.name)
	require.Equal(t, "customers_app", finalDS.username)

	// 5. SPRING_CLOUD_CONFIG_ENABLED=false disables configserver import completely.
	disabledRoot := springConfigFixture(t, map[string]string{
		"src/main/resources/application.properties": "spring.config.import=configserver:not-a-valid-url\n" +
			"spring.datasource.url=" + jdbcURL + "\nspring.datasource.username=customers_app\n",
	})
	cfgDisabled, err := loadSpringConfiguration(t.Context(), springConfigInput{
		root:        disabledRoot,
		deployed:    true,
		environment: map[string]string{"SPRING_CLOUD_CONFIG_ENABLED": "false"},
	})
	require.NoError(t, err)
	disabledDS, err := inferSpringDatasource(cfgDisabled, postgresDeps, true)
	require.NoError(t, err)
	require.Equal(t, DatabasePostgreSQL, disabledDS.engine)
}

func TestSpringProfileSelectorsExpandBeforeSplittingAcrossScalarAndIndexedForms(t *testing.T) {
	t.Run("scalar placeholder and default with commas", func(t *testing.T) {
		root := springConfigFixture(t, map[string]string{
			"src/main/resources/application.properties": "spring.profiles.active=${DEPLOY_PROFILES:prod,database}\n" +
				"spring.profiles.include=${INCLUDED_PROFILES:common,audit}\n" +
				"spring.profiles.group.prod=${PROD_GROUP:tls,cloud}\n",
			"src/main/resources/application-database.properties": "spring.datasource.username=db_user\n",
			"src/main/resources/application-cloud.properties":    "spring.datasource.username=cloud_user\n",
		})
		cfgDefault, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
		require.NoError(t, err)
		require.Equal(t, []string{"common", "audit", "prod", "tls", "cloud", "database"}, cfgDefault.profiles)

		cfgEnv, err := loadSpringConfiguration(t.Context(), springConfigInput{
			root:        root,
			environment: map[string]string{"DEPLOY_PROFILES": "prod,database", "INCLUDED_PROFILES": "metrics,trace"},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"metrics", "trace", "prod", "tls", "cloud", "database"}, cfgEnv.profiles)
	})

	t.Run("indexed YAML default active include and group lists", func(t *testing.T) {
		root := springConfigFixture(t, map[string]string{
			"src/main/resources/application.yaml": "spring:\n  profiles:\n" +
				"    default:\n      - ${DEFAULT_PROFILES:local,h2}\n" +
				"    include:\n      - ${INCLUDE_FIRST:shared,telemetry}\n" +
				"    group:\n      h2:\n        - ${H2_GROUP:embedded,seed}\n",
		})
		cfg, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root})
		require.NoError(t, err)
		require.Equal(t, []string{"shared", "telemetry", "local", "h2", "embedded", "seed"}, cfg.profiles)
	})
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
	for _, expression := range []string{"prod", "prod,cloud", "cloud,prod", "prod & !test", "(prod & us) | eu"} {
		match, err := matchesSpringProfiles(expression, []string{"prod", "us"})
		require.NoError(t, err)
		require.True(t, match)
	}
	match, err := matchesSpringProfiles("dev,staging", []string{"prod"})
	require.NoError(t, err)
	require.False(t, match)
	for _, expression := range []string{
		"prod & us | eu", "(prod", "prod || us", "", "prod,", ",prod", "prod,a & b | c",
	} {
		_, err := matchesSpringProfiles(expression, []string{"prod"})
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = loadSpringConfiguration(ctx, springConfigInput{root: t.TempDir()})
	require.ErrorIs(t, err, context.Canceled)
}
