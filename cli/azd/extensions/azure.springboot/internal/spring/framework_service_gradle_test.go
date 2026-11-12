// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func createGradleProject(t *testing.T, script string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Gradle application")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gradle", "wrapper"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	for _, filename := range []string{
		script, "gradlew", "gradlew.bat",
		filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"),
		filepath.Join("gradle", "wrapper", "gradle-wrapper.jar"),
	} {
		// #nosec G306 -- executable wrapper fixture with no credentials.
		require.NoError(t, os.WriteFile(filepath.Join(root, filename), []byte("fixture"), 0o700))
	}
	return root
}

func TestJavaBuildSelectionAndValidation(t *testing.T) {
	for _, script := range []string{"build.gradle", "build.gradle.kts"} {
		t.Run(script, func(t *testing.T) {
			root := createGradleProject(t, script)
			system, err := detectJavaBuild(root)
			require.NoError(t, err)
			require.Equal(t, gradleBuild, system)
			require.NoError(t, validateJavaBuild(root, "app"))
			require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte("invalid Maven"), 0o600))
			system, err = detectJavaBuild(root)
			require.NoError(t, err)
			require.Equal(t, mavenBuild, system)
			require.ErrorContains(t, validateJavaBuild(root, "app"), "Maven")
		})
	}
	for _, missing := range []string{
		gradleWrapperName(), "src", filepath.Join("gradle", "wrapper", "gradle-wrapper.jar"),
		filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"),
	} {
		t.Run("missing "+missing, func(t *testing.T) {
			root := createGradleProject(t, "build.gradle")
			require.NoError(t, os.Remove(filepath.Join(root, missing)))
			require.Error(t, validateGradleProject(root))
		})
	}
}

func gradleReportRunner(t *testing.T, report gradleReport, onRun func(azdexec.RunArgs)) *commandRunnerFunc {
	t.Helper()
	return &commandRunnerFunc{run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
		require.Equal(t, gradleWrapperName(), filepath.Base(args.Cmd))
		require.NotContains(t, args.Args, skipGitMetadataMavenOption)
		require.NotNil(t, args.DebugLogging)
		require.False(t, *args.DebugLogging)
		require.Contains(t, args.Args, "--no-daemon")
		if onRun != nil {
			onRun(args)
		}
		for _, arg := range args.Args {
			if filename, found := strings.CutPrefix(arg, "-Dazd.spring.report="); found {
				data, err := json.Marshal(report)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filename, data, 0o600))
				return azdexec.NewRunResult(0, "", ""), nil
			}
		}
		t.Fatal("missing private report path")
		return azdexec.RunResult{}, nil
	}}
}

func executableGradleJar(t *testing.T, major uint16) []byte {
	t.Helper()
	source := mavenJar(t, major)
	reader, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	require.NoError(t, err)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range reader.File {
		require.NoError(t, writer.Copy(file))
	}
	manifest, err := writer.Create("META-INF/MANIFEST.MF")
	require.NoError(t, err)
	_, err = manifest.Write([]byte(
		"Manifest-Version: 1.0\r\nMain-Class: org.springframework.boot.loader.launch.JarLauncher\r\nStart-Class: App\r\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}

func TestGradleFrameworkOperationsAndArtifacts(t *testing.T) {
	root := createGradleProject(t, "build.gradle.kts")
	output := filepath.Join(root, "custom output", "executable.jar")
	require.NoError(t, os.Mkdir(filepath.Dir(output), 0o700))
	require.NoError(t, os.WriteFile(output, executableGradleJar(t, java17ClassMajorVersion), 0o600))
	report := gradleReport{Version: 1, Root: root, Archive: output, Dependencies: []string{"com.h2database:h2"}}
	var operations []string
	var directories []string
	runner := gradleReportRunner(t, report, func(args azdexec.RunArgs) {
		require.Contains(t, args.Env, "CUSTOM_ENV=value")
		for _, arg := range args.Args {
			if value, found := strings.CutPrefix(arg, "-Dazd.spring.operation="); found {
				operations = append(operations, value)
			}
			if filename, found := strings.CutPrefix(arg, "-Dazd.spring.report="); found {
				directories = append(directories, filepath.Dir(filename))
			}
		}
	})
	framework := &gradleProject{runner: runner, env: environment.NewWithValues("test", map[string]string{
		"CUSTOM_ENV": "value",
	})}
	service := &project.ServiceConfig{
		Project: &project.ProjectConfig{Path: root}, RelativePath: ".", Host: project.ServiceTargetKind(SpringHost),
	}
	progress := async.NewNoopProgress[project.ServiceProgress]()
	require.NoError(t, framework.Initialize(t.Context(), service))
	_, err := framework.Restore(t.Context(), service, nil, progress)
	require.NoError(t, err)
	_, err = framework.Build(t.Context(), service, nil, progress)
	require.NoError(t, err)
	result, err := framework.Package(t.Context(), service, nil, progress)
	require.NoError(t, err)
	require.Equal(t, []string{"restore", "build", "package"}, operations)
	require.Len(t, result.Artifacts, 1)
	artifact := result.Artifacts[0]
	t.Cleanup(func() { require.NoError(t, removeGradleDirectory(artifact.Location)) })
	require.Equal(t, "gradle", artifact.Metadata["framework"])
	content, err := os.ReadFile(filepath.Join(artifact.Location, "app.jar"))
	require.NoError(t, err)
	require.Equal(t, executableGradleJar(t, java17ClassMajorVersion), content)
	for _, directory := range directories {
		require.NoDirExists(t, directory)
	}
	service.OutputPath = "wrong-output"
	before := len(operations)
	_, err = framework.Package(t.Context(), service, nil, progress)
	require.ErrorContains(t, err, "dist/output")
	require.Equal(t, []string{"restore"}, operations[before:], "invalid dist must not compile/package")
	service.OutputPath = "custom output"
	require.NoError(t, validateGradleOutput(service.OutputPath, report))
}

func TestGradleReportsRejectInvalidEvidenceAndPreserveCancellation(t *testing.T) {
	root := createGradleProject(t, "build.gradle")
	good := gradleReport{Version: 1, Root: root, Archive: filepath.Join(root, "build", "app.jar"), Dependencies: []string{}}
	for _, mutate := range []func(*gradleReport){
		func(r *gradleReport) { r.Version = 2 },
		func(r *gradleReport) { r.Root = t.TempDir() },
		func(r *gradleReport) { r.Archive = "relative.jar" },
		func(r *gradleReport) { r.Archive = filepath.Join(t.TempDir(), "outside.jar") },
		func(r *gradleReport) { r.Dependencies = nil },
		func(r *gradleReport) { r.Dependencies = []string{"invalid-coordinate"} },
	} {
		report := good
		mutate(&report)
		_, err := runGradle(t.Context(), gradleReportRunner(t, report, nil), root, nil, "restore")
		require.ErrorContains(t, err, "metadata")
	}
	for _, failure := range []error{
		context.Canceled, context.DeadlineExceeded,
		errors.New("failed https://user:password@example.org/distribution.zip?sig=private-token#private-fragment"),
	} {
		runner := &commandRunnerFunc{run: func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error) {
			return azdexec.RunResult{}, failure
		}}
		_, err := runGradle(t.Context(), runner, root, nil, "restore")
		require.Error(t, err)
		if errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) {
			require.ErrorIs(t, err, failure)
		}
		for _, secret := range []string{"user:", "password", "private-token", "private-fragment"} {
			require.NotContains(t, err.Error(), secret)
		}
	}
}

func TestGradleFrameworkSelectionIsSpringOnly(t *testing.T) {
	root, runner := setupJavaFrameworkInitializeTest(t)
	require.NoError(t, os.CopyFS(root, os.DirFS(createGradleProject(t, "build.gradle.kts"))))
	service := &azdext.ServiceConfig{Name: "app", RelativePath: ".", Host: SpringHost, Language: "java"}
	config := &azdext.ProjectConfig{Path: root, Name: "gradle", Services: map[string]*azdext.ServiceConfig{"app": service}}
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"),
		[]byte("name: gradle\nservices:\n  app:\n    project: .\n    host: springbootapp\n    language: java\n"), 0o600))
	provider := newJavaFrameworkService(packagingTestClient(t, config), runner)
	require.NoError(t, provider.Initialize(t.Context(), service))
	require.IsType(t, &gradleProject{}, provider.nativeFramework(environment.New("test")))
	service.Host = "containerapp"
	require.NoError(t, provider.Initialize(t.Context(), service))
	require.Equal(t, mavenBuild, provider.build)
	service.Host = SpringHost
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte("invalid Maven"), 0o600))
	require.NoError(t, provider.Initialize(t.Context(), service))
	require.Equal(t, mavenBuild, provider.build)
}

func TestGradleRejectsLinkedFilesAndNonExecutableUnixWrapper(t *testing.T) {
	root := createGradleProject(t, "build.gradle")
	external := filepath.Join(t.TempDir(), "outside.jar")
	require.NoError(t, os.WriteFile(external, []byte("outside"), 0o600))
	link := filepath.Join(root, "linked.jar")
	if err := os.Symlink(external, link); err != nil {
		t.Logf("symlink creation unavailable: %v", err)
	} else {
		_, err := containedGradleFile(root, link)
		require.ErrorContains(t, err, "link")
	}
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(filepath.Join(root, gradleWrapperName()), 0o600))
		require.ErrorContains(t, validateGradleProject(root), "not executable")
	}
}

func TestGradleArchiveAndArtifactSafety(t *testing.T) {
	root := createGradleProject(t, "build.gradle")
	for _, content := range [][]byte{
		[]byte("not a JAR"), mavenJar(t, java17ClassMajorVersion), executableGradleJar(t, 65),
	} {
		file := filepath.Join(root, "app.jar")
		require.NoError(t, os.WriteFile(file, content, 0o600))
		require.Error(t, validateGradleArchive(t.Context(), file))
	}
	outside := filepath.Join(t.TempDir(), "outside.jar")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	_, err := containedGradleFile(root, outside)
	require.ErrorContains(t, err, "outside")
	context := mavenPackageContext(t, executableGradleJar(t, java17ClassMajorVersion))
	context.Package[0].Metadata["framework"] = "gradle"
	project := &azdext.ProjectConfig{Path: root}
	service := &azdext.ServiceConfig{Name: "app", Host: SpringHost, RelativePath: "."}
	staged, err := prepareContainerContext(t.Context(), project, service, context)
	require.NoError(t, err)
	require.NoError(t, staged.cleanup())
	context.Package = append(context.Package, context.Package[0])
	_, err = findSpringJar(context, root)
	require.ErrorContains(t, err, "more than one Gradle")
	context.Package = context.Package[:1]
	location := context.Package[0].Location
	context.Package[0].Location = ""
	_, err = findSpringJar(context, root)
	require.ErrorContains(t, err, "no location")
	context.Package[0].Location = location
	context.Package[0].Metadata["framework"] = "maven"
	_, err = findSpringJar(context, root)
	require.ErrorContains(t, err, "different build system")
}

func TestGradleProductionDatasourceDependencies(t *testing.T) {
	require.Equal(t, springDependencies{}, springDependenciesFromCoordinates(nil))
	require.Equal(t, springDependencies{h2: true}, springDependenciesFromCoordinates([]string{"com.h2database:h2"}))
	require.Equal(t, springDependencies{database: true, postgresql: true, azureIdentity: true, azureJDBC: true},
		springDependenciesFromCoordinates([]string{"com.azure.spring:spring-cloud-azure-starter-jdbc-postgresql"}))
}
