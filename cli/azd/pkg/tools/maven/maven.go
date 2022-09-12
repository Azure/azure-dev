package maven

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	osexec "os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type MavenCli interface {
	tools.ExternalTool
	Package(ctx context.Context, projectPath string) error
	ResolveDependencies(ctx context.Context, projectPath string) error
}

type mavenCli struct {
	commandRunner exec.CommandRunner
}

func (m *mavenCli) Name() string {
	return "Maven"
}

func (m *mavenCli) InstallUrl() string {
	return "https://maven.apache.org"
}

func (m *mavenCli) jdkVersionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 17,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://jdk.java.net/ to upgrade",
	}
}

func (m *mavenCli) CheckInstalled(ctx context.Context) (bool, error) {
	javac, err := getJavaCompilerPath()
	if err != nil {
		return false, fmt.Errorf("checking java jdk installation: %s", err)
	}

	res, err := tools.ExecuteCommand(ctx, javac, "--version")
	if err != nil {
		return false, fmt.Errorf("checking javac version: %w", err)
	}

	javaSemver, err := tools.ExtractSemver(res)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}

	updateDetail := m.jdkVersionInfo()
	if javaSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: "Java JDK", VersionInfo: m.jdkVersionInfo()}
	}

	return true, nil
}

func getJavaCompilerPath() (string, error) {
	javac := "javac"
	path, err := osexec.LookPath(javac)
	if err == nil {
		return path, nil
	}

	if !errors.Is(err, osexec.ErrNotFound) {
		return "", err
	}

	home := os.Getenv("JAVA_HOME")
	if home == "" {
		return "", errors.New("java JDK not installed")
	}

	absPath := filepath.Join(home, "bin", pathOptionalExt(javac, ".exe"))
	_, err = os.Stat(absPath)
	if err == nil {
		return absPath, nil
	}

	if errors.Is(err, osexec.ErrNotFound) {
		return "", fmt.Errorf("javac could not be found under JAVA_HOME directory. Expected javac to be present at: %s", absPath)
	}

	return "", err
}

func pathOptionalExt(executable string, ext string) string {
	if runtime.GOOS == "windows" {
		return executable + ext
	} else {
		return executable
	}
}

func getMavenPath(projectPath string) (string, error) {
	mvnw, ok := getMavenWrapperPath(projectPath)
	if ok {
		return mvnw, nil
	}

	mvn, err := osexec.LookPath("mvn")
	if err != nil {
		return "", err
	}

	return mvn, nil
}

func getMavenWrapperPath(projectPath string) (string, bool) {
	mvnw := pathOptionalExt(filepath.Join(projectPath, "mvnw"), ".cmd")
	if _, err := os.Stat(mvnw); err != nil {
		return "", false
	}

	return mvnw, true
}

func (cli *mavenCli) Package(ctx context.Context, projectPath string) error {
	mvn, err := getMavenPath(projectPath)
	if err != nil {
		return err
	}
	runArgs := exec.NewRunArgs(mvn, "package").WithCwd(projectPath)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn package on project '%s' failed: %s: %w", projectPath, res.String(), err)
	}
	return nil
}

func (cli *mavenCli) ResolveDependencies(ctx context.Context, projectPath string) error {
	mvn, err := getMavenPath(projectPath)
	if err != nil {
		return err
	}

	runArgs := exec.NewRunArgs(mvn, "dependency:resolve").WithCwd(projectPath)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn dependency:resolve on project '%s' failed: %s: %w", projectPath, res.String(), err)
	}
	return nil
}

func NewMavenCli(ctx context.Context) MavenCli {
	return &mavenCli{
		commandRunner: exec.GetCommandRunner(ctx),
	}
}
