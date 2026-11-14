// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_CLI_SpringGradle_Package(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_GRADLE_PACKAGE") != "1" {
		t.Skip("set AZD_TEST_SPRING_GRADLE_PACKAGE=1 to run the local Gradle container package gate")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	gradleHome := tempDirWithDiagnostics(t)
	for _, dsl := range []string{"groovy", "kotlin"} {
		t.Run(dsl, func(t *testing.T) {
			root := stageGradleApplication(t, ctx, dsl)
			cli := newSpringCLI(t)
			cli.WorkingDirectory = root
			cli.Env = append(environmentWithout(cli.Env, "AZD_CONFIG_DIR", "GRADLE_USER_HOME"),
				"AZD_CONFIG_DIR="+configDir, "GRADLE_USER_HOME="+gradleHome)
			envName := "gradle-" + dsl + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			registerNativeImageCleanup(t, "gradle-spring/app-"+envName+":")
			result, err := cli.RunCommand(ctx, "init", "-e", envName, "--no-prompt")
			require.NoError(t, err, result.Stdout+result.Stderr)
			archive := filepath.Join(root, "build", "custom-package", "application-executable.jar")
			require.NoFileExists(t, archive, "initial packaging must build the application, not reuse an existing JAR")
			for attempt := range 2 {
				result, err = cli.RunCommand(ctx, "package", "--no-prompt", "--output", "json")
				require.NoError(t, err, result.Stdout+result.Stderr)
				var packaged struct {
					Services map[string]struct {
						Artifacts []struct {
							Kind         string            `json:"kind"`
							Location     string            `json:"location"`
							LocationKind string            `json:"locationKind"`
							Metadata     map[string]string `json:"metadata"`
						} `json:"artifacts"`
					} `json:"services"`
				}
				require.NoError(t, json.Unmarshal([]byte(result.Stdout), &packaged))
				service, found := packaged.Services["app"]
				require.True(t, found)
				var images, packages []string
				for _, artifact := range service.Artifacts {
					if artifact.Kind == "container" && artifact.LocationKind == "local" {
						images = append(images, artifact.Location)
					}
					if artifact.Kind == "directory" && artifact.Metadata["framework"] == "gradle" {
						packages = append(packages, artifact.Location)
					}
				}
				require.Len(t, images, 1)
				require.Len(t, packages, 1)
				bootJar, err := os.ReadFile(archive)
				require.NoError(t, err)
				normalized, err := os.ReadFile(filepath.Join(packages[0], "app.jar"))
				require.NoError(t, err)
				require.Equal(t, sha256.Sum256(bootJar), sha256.Sum256(normalized))
				registerGradlePackageCleanup(t, packages[0], sha256.Sum256(normalized))
				if attempt == 0 {
					verifyH2ContainerStartupAt(t, ctx, images[0], "8080", "/actuator/health")
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(archive), "newest-plain.jar"),
						[]byte("this is not an executable archive"), 0o600))
				}
			}
			require.NoFileExists(t, filepath.Join(root, "Dockerfile"))
			require.NoDirExists(t, filepath.Join(root, ".azd-springboot"))
			if dsl == "groovy" {
				buildFile := filepath.Join(root, "build.gradle")
				original, err := os.ReadFile(buildFile)
				require.NoError(t, err)
				for _, test := range []struct {
					name   string
					suffix string
				}{
					{"disabled bootJar", "\ntasks.named('bootJar') { enabled = false }\n"},
					{"skipped bootJar", "\ntasks.named('bootJar') { onlyIf { false } }\n"},
					{"unresolved production dependency", "\ndependencies { runtimeOnly 'azd.missing:nonexistent:0' }\n"},
				} {
					t.Run(test.name, func(t *testing.T) {
						// #nosec G703 -- fixed build.gradle in this test's newly created application directory.
						require.NoError(t, os.WriteFile(buildFile, append(bytes.Clone(original), test.suffix...), 0o600))
						result, err := cli.RunCommand(ctx, "package", "--no-prompt")
						require.Error(t, err, result.Stdout+result.Stderr)
						// #nosec G703 -- restore the same fixed test-owned build.gradle, never a downloaded path.
						require.NoError(t, os.WriteFile(buildFile, original, 0o600))
					})
				}
				t.Run("multi-project build", func(t *testing.T) {
					settings := filepath.Join(root, "settings.gradle")
					require.NoError(t, os.WriteFile(settings,
						[]byte("rootProject.name = 'gradle-application'\ninclude('child')\n"), 0o600))
					require.NoError(t, os.Mkdir(filepath.Join(root, "child"), 0o700))
					result, err := cli.RunCommand(ctx, "package", "--no-prompt")
					require.Error(t, err, result.Stdout+result.Stderr)
					require.NoError(t, os.WriteFile(settings,
						[]byte("rootProject.name = 'gradle-application'\n"), 0o600))
				})
				t.Run("test-only H2 is not runtime evidence", func(t *testing.T) {
					testOnly := strings.ReplaceAll(string(original),
						"runtimeOnly 'com.h2database:h2'", "testRuntimeOnly 'com.h2database:h2'")
					// #nosec G703 -- fixed build.gradle in this test's newly created application directory.
					require.NoError(t, os.WriteFile(buildFile, []byte(testOnly), 0o600))
					result, err := cli.RunCommand(ctx, "package", "--no-prompt")
					require.Error(t, err, result.Stdout+result.Stderr)
					// #nosec G703 -- restore the fixed test-owned build.gradle, never a downloaded path.
					require.NoError(t, os.WriteFile(buildFile, original, 0o600))
				})
			}
		})
	}
}

func registerGradlePackageCleanup(t *testing.T, directory string, hash [sha256.Size]byte) {
	t.Helper()
	require.True(t, strings.HasPrefix(filepath.Base(directory), "azd-springboot-gradle-package-"))
	original, err := os.Lstat(directory)
	require.NoError(t, err)
	require.True(t, original.IsDir())
	t.Cleanup(func() {
		current, err := os.Lstat(directory)
		if os.IsNotExist(err) {
			return
		}
		require.NoError(t, err)
		require.True(t, os.SameFile(original, current), "refuse cleanup of a replaced package directory")
		filename := filepath.Join(directory, "app.jar")
		content, err := os.ReadFile(filename)
		require.NoError(t, err)
		require.Equal(t, hash, sha256.Sum256(content), "refuse cleanup of a modified package")
		require.NoError(t, os.Remove(filename))
		require.NoError(t, os.Remove(directory))
	})
}

func stageGradleApplication(t *testing.T, ctx context.Context, dsl string) string {
	t.Helper()
	root := tempDirWithDiagnostics(t)
	files := map[string]string{
		"azure.yaml": "name: gradle-spring\ninfra:\n  provider: bicep\nservices:\n" +
			"  app:\n    project: .\n    language: java\n    host: springbootapp\n",
		filepath.Join("infra", "app.bicep"):           "// Local package-only fixture; no Azure resources.\n",
		filepath.Join("infra", "app.parameters.json"): `{"parameters":{}}`,
		filepath.Join("src", "main", "resources", "application.properties"): "spring.datasource.url=" +
			"jdbc:h2:mem:demo;DB_CLOSE_DELAY=-1\nspring.datasource.username=sa\n",
		filepath.Join("src", "main", "java", "demo", "Application.java"): `package demo;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
@SpringBootApplication
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }
}
`,
		filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"): "distributionUrl=" +
			"https\\://services.gradle.org/distributions/gradle-8.14.4-bin.zip\n" +
			"distributionSha256Sum=f1771298a70f6db5a29daf62378c4e18a17fc33c9ba6b14362e0cdf40610380d\n",
	}
	if dsl == "groovy" {
		files["settings.gradle"] = "rootProject.name = 'gradle-application'\n"
		files["build.gradle"] = `plugins {
    id 'java'
    id 'org.springframework.boot' version '3.5.13'
}
repositories { mavenCentral() }
dependencies {
    implementation platform('org.springframework.boot:spring-boot-dependencies:3.5.13')
    implementation 'org.springframework.boot:spring-boot-starter-web'
    implementation 'org.springframework.boot:spring-boot-starter-actuator'
    implementation 'org.springframework.boot:spring-boot-starter-jdbc'
    runtimeOnly 'com.h2database:h2'
}
tasks.withType(JavaCompile).configureEach { options.release = 17 }
tasks.named('bootJar') {
    archiveFileName = 'application-executable.jar'
    destinationDirectory = layout.buildDirectory.dir('custom-package')
}
`
	} else {
		files["settings.gradle.kts"] = "rootProject.name = \"gradle-application\"\n"
		files["build.gradle.kts"] = `import org.springframework.boot.gradle.tasks.bundling.BootJar
plugins {
    java
    id("org.springframework.boot") version "3.5.13"
}
repositories { mavenCentral() }
dependencies {
    implementation(platform("org.springframework.boot:spring-boot-dependencies:3.5.13"))
    implementation("org.springframework.boot:spring-boot-starter-web")
    implementation("org.springframework.boot:spring-boot-starter-actuator")
    implementation("org.springframework.boot:spring-boot-starter-jdbc")
    runtimeOnly("com.h2database:h2")
}
tasks.withType<JavaCompile>().configureEach { options.release.set(17) }
tasks.named<BootJar>("bootJar") {
    archiveFileName.set("application-executable.jar")
    destinationDirectory.set(layout.buildDirectory.dir("custom-package"))
}
`
	}
	for name, content := range files {
		destination := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o700))
		require.NoError(t, os.WriteFile(destination, []byte(content), 0o600))
	}
	for _, file := range []struct {
		name string
		hash string
	}{
		{"gradlew", "fb49f8cb2e5b1d83fba3dcc2c0dd0934c5655cbaf8bd1510ada7cc02692095ca"},
		{"gradlew.bat", "49792578a7942e08b708df3d3902ddbfc49d2203775c0161ef0c24b23fe4aeae"},
		{"gradle/wrapper/gradle-wrapper.jar", "7d3a4ac4de1c32b59bc6a4eb8ecb8e612ccd0cf1ae1e99f66902da64df296172"},
	} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://raw.githubusercontent.com/gradle/gradle/v8.14.4/"+file.name, nil)
		require.NoError(t, err)
		response, err := (&http.Client{Timeout: time.Minute}).Do(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
		hash := sha256.Sum256(data)
		require.Equal(t, file.hash, hex.EncodeToString(hash[:]), "immutable Gradle wrapper fixture changed")
		// #nosec G306 -- official, checksum-verified executable Gradle wrapper fixture.
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(file.name)), data, 0o700))
	}
	return root
}
