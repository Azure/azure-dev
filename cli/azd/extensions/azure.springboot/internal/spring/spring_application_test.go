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
	// #nosec G304 -- fixed filename inside t.TempDir().
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
<dependency><groupId>com.h2database</groupId><artifactId>h2</artifactId><scope>system</scope></dependency>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId><scope>provided</scope></dependency>
<dependency>
<groupId>com.azure</groupId><artifactId>azure-identity-extensions</artifactId><scope>provided</scope>
</dependency>
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

func TestDatabaseFreeDependencyEvidenceRejectsUnresolvedMavenInputs(t *testing.T) {
	for _, pom := range []string{
		`<project><dependencies><dependency><groupId>${group}</groupId>
			<artifactId>spring-boot-starter-web</artifactId></dependency></dependencies></project>`,
		`<project><parent><groupId>example</groupId><artifactId>custom-parent</artifactId></parent></project>`,
		`<project><dependencies><dependency><groupId>org.springframework.boot</groupId>
			<artifactId>spring-boot-starter-web</artifactId><scope>${scope}</scope></dependency></dependencies></project>`,
	} {
		root := springConfigFixture(t, map[string]string{"pom.xml": pom})
		_, err := readSpringDependencies(t.Context(), root)
		require.ErrorContains(t, err, "packaged JAR evidence")
	}
}

func TestMultiModuleParentPOMDependenciesResolveWithinProjectRoot(t *testing.T) {
	root := springConfigFixture(t, map[string]string{
		"pom.xml": `<project>
			<parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId></parent>
			<groupId>org.springframework.samples</groupId>
			<artifactId>spring-petclinic-multiservice</artifactId>
			<packaging>pom</packaging>
		</project>`,
		"petclinic-web/pom.xml": `<project>
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
		</project>`,
	})
	dependencies, err := readSpringDependenciesInProject(
		t.Context(), root, filepath.Join(root, "petclinic-web"),
	)
	require.NoError(t, err)
	require.Equal(t, springDependencies{}, dependencies)
}

func TestPackagedJDBCEvidenceIsNotDatabaseFree(t *testing.T) {
	for _, dependency := range []string{
		"spring-jdbc-6.jar", "spring-data-jpa-3.jar", "mysql-connector-j-9.jar", "hibernate-core-6.jar",
	} {
		archive, err := openSpringArchive(springJarFixture(t, [][2]string{
			{"BOOT-INF/classes/application.properties", "server.port=8080"},
			{"BOOT-INF/lib/" + dependency, ""},
		}))
		require.NoError(t, err)
		require.NoError(t, archive.reader.Close())
		_, err = inferSpringDatasource(datasourceProperties(t, ""), archive.dependencies, true)
		require.Error(t, err)
	}

	validatorArchive, err := openSpringArchive(springJarFixture(t, [][2]string{
		{"BOOT-INF/classes/application.properties", "server.port=8080"},
		{"BOOT-INF/lib/hibernate-validator-9.1.3.Final.jar", ""},
	}))
	require.NoError(t, err)
	require.NoError(t, validatorArchive.reader.Close())
	discovered, err := inferSpringDatasource(datasourceProperties(t, ""), validatorArchive.dependencies, true)
	require.NoError(t, err)
	require.Equal(t, databaseNone, discovered.engine)
}

func writeCustomJar(t *testing.T, path string, entries [][2]string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	// #nosec G304 -- path is constructed by test callers inside t.TempDir().
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		resource, createErr := writer.Create(entry[0])
		require.NoError(t, createErr)
		_, writeErr := resource.Write([]byte(entry[1]))
		require.NoError(t, writeErr)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
}

func TestEmbeddedJarDependenciesClassifyDrivers(t *testing.T) {
	root := t.TempDir()
	localPGJar := filepath.Join(root, "libs", "custom-pg-bundle.jar")
	writeCustomJar(t, localPGJar, [][2]string{
		{"org/postgresql/Driver.class", "bytecode"},
		{"com/azure/identity/extensions/jdbc/postgresql/Plugin.class", "bytecode"},
	})
	// #nosec G304 -- localPGJar is created inside t.TempDir() immediately above.
	nestedBytes, err := os.ReadFile(localPGJar)
	require.NoError(t, err)
	bootArchive, err := openSpringArchive(springJarFixture(t, [][2]string{
		{"BOOT-INF/classes/application.properties", "server.port=8080"},
		{"BOOT-INF/lib/custom-pg-bundle.jar", string(nestedBytes)},
	}))
	require.NoError(t, err)
	require.NoError(t, bootArchive.reader.Close())
	require.True(t, bootArchive.dependencies.postgresql)
	require.True(t, bootArchive.dependencies.azureIdentity)

	vendorJar := filepath.Join(root, "libs", "vendor-driver.jar")
	writeCustomJar(t, vendorJar, [][2]string{
		{"META-INF/services/java.sql.Driver", "com.vendor.Driver\n"},
	})
	// #nosec G304 -- vendorJar is created inside t.TempDir() immediately above.
	vendorBytes, err := os.ReadFile(vendorJar)
	require.NoError(t, err)
	vendorArchive, err := openSpringArchive(springJarFixture(t, [][2]string{
		{"BOOT-INF/classes/application.properties", "server.port=8080"},
		{"BOOT-INF/lib/vendor-driver.jar", string(vendorBytes)},
	}))
	require.NoError(t, err)
	require.NoError(t, vendorArchive.reader.Close())
	require.True(t, vendorArchive.dependencies.database)
	_, err = inferSpringDatasource(datasourceProperties(t, ""), vendorArchive.dependencies, true)
	require.Error(t, err)
}
