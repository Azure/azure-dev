// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

func TestToMavenProject(t *testing.T) {
	path, err := osexec.LookPath("java")
	if err != nil {
		t.Skip("Skip readMavenProject because java command doesn't exist.")
	} else {
		slog.Info("Java command found.", "path", path)
	}
	path, err = osexec.LookPath("mvn")
	if err != nil {
		t.Skip("Skip readMavenProject because mvn command doesn't exist.")
	} else {
		slog.Info("Java command found.", "path", path)
	}
	tests := []struct {
		name     string
		testPoms []testPom
		expected []dependency
	}{
		{
			name: "Test with two dependencies",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.springframework</groupId>
									<artifactId>spring-core</artifactId>
									<version>5.3.8</version>
									<scope>compile</scope>
								</dependency>
								<dependency>
									<groupId>junit</groupId>
									<artifactId>junit</artifactId>
									<version>4.13.2</version>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.springframework",
					ArtifactId: "spring-core",
					Version:    "5.3.8",
					Scope:      "compile",
				},
				{
					GroupId:    "junit",
					ArtifactId: "junit",
					Version:    "4.13.2",
					Scope:      "test",
				},
			},
		},
		{
			name: "Test with no dependencies",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{},
		},
		{
			name: "Test with one dependency which version is decided by dependencyManagement",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
							<dependencyManagement>
								<dependencies>
									<dependency>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-dependencies</artifactId>
										<version>3.0.0</version>
										<type>pom</type>
										<scope>import</scope>
									</dependency>
								</dependencies>
							</dependencyManagement>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.slf4j",
					ArtifactId: "slf4j-api",
					Version:    "2.0.4",
					Scope:      "compile",
				},
			},
		},
		{
			name: "Test with one dependency which version is decided by parent",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.0.0</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{
				{
					GroupId:    "org.slf4j",
					ArtifactId: "slf4j-api",
					Version:    "2.0.4",
					Scope:      "compile",
				},
			},
		},
		{
			name: "Test pom with multi modules: root pom build first when run help:effective-pom",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.2.7</version>
							</parent>
							<groupId>org.springframework</groupId>
							<artifactId>gs-multi-module</artifactId>
							<version>0.1.0</version>
							<packaging>pom</packaging>
							<modules>
								<module>library</module>
								<module>application</module>
							</modules>
						</project>
						`,
				},
				{
					pomFilePath: filepath.Join("application", "pom.xml"),
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<parent>
								<groupId>org.springframework</groupId>
								<artifactId>gs-multi-module</artifactId>
								<version>0.1.0</version>
							</parent>
							<groupId>com.example</groupId>
							<artifactId>application</artifactId>
							<version>0.0.1-SNAPSHOT</version>
							<name>application</name>
							<description>Demo project for Spring Boot</description>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
							<build>
								<plugins>
									<plugin>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-maven-plugin</artifactId>
									</plugin>
								</plugins>
							</build>
						</project>
						`,
				},
				{
					pomFilePath: filepath.Join("library", "pom.xml"),
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<parent>
								<groupId>org.springframework</groupId>
								<artifactId>gs-multi-module</artifactId>
								<version>0.1.0</version>
							</parent>
							<groupId>com.example</groupId>
							<artifactId>library</artifactId>
							<version>0.0.1-SNAPSHOT</version>
							<name>library</name>
							<description>Demo project for Spring Boot</description>
							<dependencies>
								<dependency>
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot</artifactId>
								</dependency>
								<dependency>
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot-starter-test</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{},
		},
		{
			name: "Test pom with multi modules: root pom build last when run help:effective-pom",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>org.springframework</groupId>
							<artifactId>gs-multi-module</artifactId>
							<version>0.1.0</version>
							<packaging>pom</packaging>
							<modules>
								<module>library</module>
								<module>application</module>
							</modules>
						</project>
						`,
				},
				{
					pomFilePath: filepath.Join("application", "pom.xml"),
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.3.0</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
							<groupId>com.example</groupId>
							<artifactId>application</artifactId>
							<version>0.0.1-SNAPSHOT</version>
							<name>application</name>
							<description>Demo project for Spring Boot</description>
							<dependencies>
								<dependency>
									<groupId>org.slf4j</groupId>
									<artifactId>slf4j-api</artifactId>
								</dependency>
							</dependencies>
							<build>
								<plugins>
									<plugin>
										<groupId>org.springframework.boot</groupId>
										<artifactId>spring-boot-maven-plugin</artifactId>
									</plugin>
								</plugins>
							</build>
						</project>
						`,
				},
				{
					pomFilePath: filepath.Join("library", "pom.xml"),
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<parent>
								<groupId>org.springframework.boot</groupId>
								<artifactId>spring-boot-starter-parent</artifactId>
								<version>3.2.2</version>
								<relativePath/> <!-- lookup parent from repository -->
							</parent>
							<groupId>com.example</groupId>
							<artifactId>library</artifactId>
							<version>0.0.1-SNAPSHOT</version>
							<name>library</name>
							<description>Demo project for Spring Boot</description>
							<dependencies>
								<dependency>
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot</artifactId>
								</dependency>
								<dependency>
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot-starter-test</artifactId>
									<scope>test</scope>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: []dependency{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			workingDir, err := prepareTestPomFiles(tt.testPoms)
			if err != nil {
				t.Fatalf("%v", err)
			}
			testPom := tt.testPoms[0]
			pomFilePath := filepath.Join(workingDir, testPom.pomFilePath)

			mavenProject, err := readMavenProject(context.TODO(), maven.NewCli(exec.NewCommandRunner(nil)),
				pomFilePath)
			if err != nil {
				t.Fatalf("readMavenProject failed: %v", err)
			}

			if len(mavenProject.Dependencies) != len(tt.expected) {
				t.Fatalf("Expected: %d\nActual: %d", len(tt.expected), len(mavenProject.Dependencies))
			}

			for i, dep := range mavenProject.Dependencies {
				if dep != tt.expected[i] {
					t.Errorf("\nExpected: %s\nActual:   %s", tt.expected[i], dep)
				}
			}
		})
	}
}

func TestIsGradleDependency(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		groupId    string
		artifactId string
		expected   bool
	}{
		{
			name:       "Single quote colon notation",
			line:       "implementation 'com.mysql:mysql-connector-j:8.0.28'",
			groupId:    "com.mysql",
			artifactId: "mysql-connector-j",
			expected:   true,
		},
		{
			name:       "Double quote colon notation",
			line:       "implementation \"org.postgresql:postgresql:42.3.1\"",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   true,
		},
		{
			name:       "Implementation with parentheses",
			line:       "implementation(\"com.mysql:mysql-connector-j:8.0.28\")",
			groupId:    "com.mysql",
			artifactId: "mysql-connector-j",
			expected:   true,
		},
		{
			name:       "Different configuration",
			line:       "runtimeOnly 'org.postgresql:postgresql:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   true,
		},
		{
			name:       "Map notation with single quotes",
			line:       "implementation group: 'com.mysql', name: 'mysql-connector-j', version: '8.0.28'",
			groupId:    "com.mysql",
			artifactId: "mysql-connector-j",
			expected:   true,
		},
		{
			name:       "Map notation with double quotes",
			line:       "implementation group: \"org.postgresql\", name: \"postgresql\", version: \"42.3.1\"",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   true,
		},
		{
			name:       "With extra spaces",
			line:       "implementation   group:  'org.postgresql',  name:   'postgresql',  version:  '42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   true,
		},
		{
			name:       "Not matching group",
			line:       "implementation 'org.other:postgresql:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   false,
		},
		{
			name:       "Not matching artifactId",
			line:       "implementation 'org.postgresql:other-driver:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   false,
		},
		{
			name:       "Comment line",
			line:       "// implementation 'org.postgresql:postgresql:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   false,
		},
		{
			name:       "Not a dependency declaration",
			line:       "String postgresqlDependency = 'org.postgresql:postgresql:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   false,
		},
		{
			name:       "Kotlin DSL format",
			line:       "implementation(\"org.postgresql:postgresql:42.3.1\")",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   true,
		},
		{
			name:       "Missing configuration keyword",
			line:       "'org.postgresql:postgresql:42.3.1'",
			groupId:    "org.postgresql",
			artifactId: "postgresql",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGradleDependency(tt.line, tt.groupId, tt.artifactId)
			if result != tt.expected {
				t.Errorf("isGradleDependency(%q, %q, %q) = %v, expected %v",
					tt.line, tt.groupId, tt.artifactId, result, tt.expected)
			}
		})
	}
}

type testPom struct {
	pomFilePath      string
	pomContentString string
}

func prepareTestPomFiles(testPoms []testPom) (string, error) {
	tempDir, err := os.MkdirTemp("", "prepareTestPomFiles")
	if err != nil {
		return "", err
	}
	for _, testPom := range testPoms {
		pomPath := filepath.Join(tempDir, testPom.pomFilePath)
		err := os.MkdirAll(filepath.Dir(pomPath), 0755)
		if err != nil {
			return "", err
		}
		err = os.WriteFile(pomPath, []byte(testPom.pomContentString), 0600)
		if err != nil {
			return "", err
		}
	}
	return tempDir, nil
}
