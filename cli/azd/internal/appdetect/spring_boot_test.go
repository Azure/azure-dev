package appdetect

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

func TestGetDatabaseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"jdbc:postgresql://localhost:5432/your-database-name", "your-database-name"},
		{"jdbc:postgresql://remote_host:5432/your-database-name", "your-database-name"},
		{"jdbc:postgresql://your_postgresql_server:5432/your-database-name?sslmode=require", "your-database-name"},
		{
			"jdbc:postgresql://your_postgresql_server.postgres.database.azure.com:5432/your-database-name?sslmode=require",
			"your-database-name",
		},
		{
			"jdbc:postgresql://your_postgresql_server:5432/your-database-name?user=your_username&password=your_password",
			"your-database-name",
		},
		{
			"jdbc:postgresql://your_postgresql_server.postgres.database.azure.com:5432/your-database-name" +
				"?sslmode=require&spring.datasource.azure.passwordless-enabled=true", "your-database-name",
		},
	}
	for _, test := range tests {
		result := getDatabaseName(test.input)
		if result != test.expected {
			t.Errorf("For input '%s', expected '%s', but got '%s'", test.input, test.expected, result)
		}
	}
}

func TestIsValidDatabaseName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"InvalidNameWithUnderscore", "invalid_name", false},
		{"TooShortName", "sh", false},
		{
			"TooLongName", "this-name-is-way-too-long-to-be-considered-valid-" +
				"because-it-exceeds-sixty-three-characters", false,
		},
		{"InvalidStartWithHyphen", "-invalid-start", false},
		{"InvalidEndWithHyphen", "invalid-end-", false},
		{"ValidName", "valid-name", true},
		{"ValidNameWithNumbers", "valid123-name", true},
		{"ValidNameWithOnlyLetters", "valid-name", true},
		{"ValidNameWithOnlyNumbers", "123456", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IsValidDatabaseName(test.input)
			if result != test.expected {
				t.Errorf("For input '%s', expected %v, but got %v", test.input, test.expected, result)
			}
		})
	}
}

func TestDetectDependencyAboutEmbeddedWebServer(t *testing.T) {
	tests := []struct {
		name     string
		testPoms []testPom
		expected bool
	}{
		{
			name: "no web dependency",
			testPoms: []testPom{
				{
					pomFilePath: "pom.xml",
					pomContentString: `
						<project>
							<modelVersion>4.0.0</modelVersion>
							<groupId>com.example</groupId>
							<artifactId>example-project</artifactId>
							<version>1.0.0</version>
						</project>
						`,
				},
			},
			expected: false,
		},
		{
			name: "has dependency: spring-boot-starter-web",
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
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot-starter-web</artifactId>
									<version>3.0.0</version>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: true,
		},
		{
			name: "has dependency: spring-boot-starter-webflux",
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
									<groupId>org.springframework.boot</groupId>
									<artifactId>spring-boot-starter-webflux</artifactId>
									<version>3.0.0</version>
								</dependency>
							</dependencies>
						</project>
						`,
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir, err := prepareTestPomFiles(tt.testPoms)
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, testPom := range tt.testPoms {
				pomFilePath := filepath.Join(workingDir, testPom.pomFilePath)
				mavenProject, err := createMavenProject(context.TODO(), maven.NewCli(exec.NewCommandRunner(nil)),
					pomFilePath)
				if err != nil {
					t.Fatalf("%v", err)
				}
				project := Project{
					Language:      Java,
					Path:          pomFilePath,
					DetectionRule: "Inferred by presence of: pom.xml",
				}
				detectAzureDependenciesByAnalyzingSpringBootProject(mavenProject, &project)
				if project.Metadata.ContainsDependencyAboutEmbeddedWebServer != tt.expected {
					t.Errorf("\nExpected: %v\nActual:   %v", tt.expected,
						project.Metadata.ContainsDependencyAboutEmbeddedWebServer)
				}
			}
		})
	}
}
