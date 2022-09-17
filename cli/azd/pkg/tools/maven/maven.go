package maven

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	osexec "os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type MavenCli interface {
	tools.ExternalTool
	Package(ctx context.Context, projectPath string) error
	ResolveDependencies(ctx context.Context, projectPath string) error
}

type mavenCli struct {
	commandRunner   exec.CommandRunner
	projectPath     string
	rootProjectPath string

	// Lazily initialized. Access through mvnCmd.
	mvnCmdStr  string
	mvnCmdOnce sync.Once
	mvnCmdErr  error
}

func (m *mavenCli) Name() string {
	return "Maven"
}

func (m *mavenCli) InstallUrl() string {
	return "https://maven.apache.org"
}

func (m *mavenCli) CheckInstalled(ctx context.Context) (bool, error) {
	_, err := m.mvnCmd()
	if err != nil {
		return false, err
	}

	return true, nil
}

func (m *mavenCli) mvnCmd() (string, error) {
	m.mvnCmdOnce.Do(func() {
		mvn, err := getMavenPath(m.projectPath, m.rootProjectPath)
		if err != nil {
			m.mvnCmdErr = err
		} else {
			m.mvnCmdStr = mvn
		}
	})

	if m.mvnCmdErr != nil {
		return "", m.mvnCmdErr
	}

	return m.mvnCmdStr, nil
}

func getMavenPath(projectPath string, rootProjectPath string) (string, error) {
	mvnw, err := getMavenWrapperPath(projectPath, rootProjectPath)
	if mvnw != "" {
		return mvnw, nil
	}

	if err != nil {
		return "", err
	}

	mvn, err := osexec.LookPath("mvn")
	if err == nil {
		return mvn, nil
	}

	if !errors.Is(err, osexec.ErrNotFound) {
		return "", err
	}

	return "", errors.New("mvn could not be found in PATH or as mvnw in the project repository")
}

// getMavenWrapperPath finds the path to mvnw in the project directory, up to the root project directory.
//
// An error is returned if an unexpected error occurred while finding. If mvnw is not found, an empty string is returned with no error.
func getMavenWrapperPath(projectPath string, rootProjectPath string) (string, error) {
	searchDir, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}

	root, err := filepath.Abs(rootProjectPath)
	if err != nil {
		return "", err
	}

	for {
		mvnw, err := osexec.LookPath(filepath.Join(searchDir, "mvnw"))
		if err == nil {
			return mvnw, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		searchDir = filepath.Dir(searchDir)

		// Past root, terminate search and return not found
		if len(searchDir) < len(root) {
			return "", nil
		}
	}
}

func (cli *mavenCli) Package(ctx context.Context, projectPath string) error {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return err
	}
	runArgs := exec.NewRunArgs(mvnCmd, "package").WithCwd(projectPath)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn package on project '%s' failed: %s: %w", projectPath, res.String(), err)
	}
	return nil
}

func (cli *mavenCli) ResolveDependencies(ctx context.Context, projectPath string) error {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return err
	}
	runArgs := exec.NewRunArgs(mvnCmd, "dependency:resolve").WithCwd(projectPath)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn dependency:resolve on project '%s' failed: %s: %w", projectPath, res.String(), err)
	}
	return nil
}

func NewMavenCli(ctx context.Context, projectPath string, rootProjectPath string) MavenCli {
	return &mavenCli{
		commandRunner:   exec.GetCommandRunner(ctx),
		projectPath:     projectPath,
		rootProjectPath: rootProjectPath,
	}
}
