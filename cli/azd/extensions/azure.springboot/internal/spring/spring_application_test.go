// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func springJarFixture(t *testing.T, entries [][2]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "app.jar")
	file, err := os.Create(name)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		resource, err := writer.Create(entry[0])
		require.NoError(t, err)
		_, err = resource.Write([]byte(entry[1]))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	return name
}

func TestSpringPackagedResourcesReplaceHostSource(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"src/main/resources/application.properties": "spring.datasource.username=source-only\n",
	})
	jar := springJarFixture(t, [][2]string{
		{"BOOT-INF/classes/application.properties", "spring.profiles.active=prod\nspring.config.import=config/shared.yaml"},
		{"BOOT-INF/classes/config/shared.yaml", "spring:\n  datasource:\n    username: packaged\n"},
		{"BOOT-INF/classes/config/shared-prod.yaml", "spring:\n  datasource:\n    username: packaged-profile\n"},
		{"BOOT-INF/lib/postgresql-42.jar", ""},
		{"BOOT-INF/lib/azure-identity-extensions-1.jar", ""},
	})
	config, err := loadSpringConfiguration(t.Context(), springConfigInput{root: root, jarPath: jar, deployed: true})
	require.NoError(t, err)
	value, present, err := config.resolve("spring.datasource.username", false)
	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, "packaged-profile", value)
	require.NotNil(t, config.packaged)
	require.True(t, config.packaged.postgresql)
	require.True(t, config.packaged.azureIdentity)
	require.False(t, config.packaged.h2)
}

func TestSpringPackagedResourcesRejectAmbiguityAndTraversal(t *testing.T) {
	for name, entries := range map[string][][2]string{
		"duplicate": {
			{"BOOT-INF/classes/application.properties", "key=one"},
			{"BOOT-INF/classes/application.properties", "key=two"},
		},
		"traversal": {{"BOOT-INF/classes/../application.properties", "key=outside"}},
		"not boot":  {{"application.properties", "key=unsupported"}},
	} {
		t.Run(name, func(t *testing.T) {
			archive, err := openSpringArchive(springJarFixture(t, entries))
			require.Error(t, err)
			require.Nil(t, archive)
		})
	}
}

func TestSpringRuntimeDependenciesExcludeTestScope(t *testing.T) {
	root := springConfigFixture(t, map[string]string{"pom.xml": `<project><dependencies>
<dependency><groupId>com.h2database</groupId><artifactId>h2</artifactId><scope>test</scope></dependency>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId><scope>runtime</scope></dependency>
<dependency><groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId></dependency>
</dependencies></project>`})
	dependencies, err := readSpringDependencies(t.Context(), root)
	require.NoError(t, err)
	require.False(t, dependencies.h2)
	require.True(t, dependencies.postgresql)
	require.True(t, dependencies.azureIdentity)
}

func TestSpringProfileDependenciesRequirePackagedEvidenceOnlyForNonTestScope(t *testing.T) {
	for _, test := range []struct {
		name        string
		scope       string
		expectedErr string
	}{
		{name: "test dependency", scope: "test"},
		{name: "runtime dependency", scope: "runtime", expectedErr: "packaged JAR evidence"},
		{name: "default dependency", expectedErr: "packaged JAR evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := springConfigFixture(t, map[string]string{"pom.xml": `<project><profiles><profile><dependencies>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId><scope>` +
				test.scope + `</scope></dependency>
</dependencies></profile></profiles></project>`})
			dependencies, err := readSpringDependencies(t.Context(), root)
			if test.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, springDependencies{}, dependencies)
			} else {
				require.ErrorContains(t, err, test.expectedErr)
			}
		})
	}
}
