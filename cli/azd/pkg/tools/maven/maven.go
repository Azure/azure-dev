package maven

import (
	"context"
	"errors"
	"fmt"
	"log"
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
		mvnCmd, err := getMavenPath(m.projectPath, m.rootProjectPath)
		if err != nil {
			m.mvnCmdErr = err
		} else {
			m.mvnCmdStr = mvnCmd
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
		return "", fmt.Errorf("failed finding mvnw in repository path: %w", err)
	}

	mvn, err := osexec.LookPath("mvn")
	if err == nil {
		return mvn, nil
	}

	if !errors.Is(err, osexec.ErrNotFound) {
		return "", fmt.Errorf("failed looking up mvn in PATH: %w", err)
	}

	return "", errors.New(
		"maven could not be found. Install either Maven or Maven Wrapper by " +
			"visiting https://maven.apache.org/ or https://maven.apache.org/wrapper/",
	)
}

// getMavenWrapperPath finds the path to mvnw in the project directory, up to the root project directory.
//
// An error is returned if an unexpected error occurred while finding.
// If mvnw is not found, an empty string is returned with
// no error.
func getMavenWrapperPath(projectPath string, rootProjectPath string) (string, error) {
	searchDir, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}

	root, err := filepath.Abs(rootProjectPath)
	log.Printf("root: %s\n", root)

	if err != nil {
		return "", err
	}

	for {
		log.Printf("searchDir: %s\n", searchDir)

		mvnw, err := osexec.LookPath(filepath.Join(searchDir, "mvnw"))
		if err == nil {
			log.Printf("found mvnw as: %s\n", mvnw)
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

	// Maven's package phase includes tests by default. Skip it explicitly.
	runArgs := exec.NewRunArgs(mvnCmd, "package", "-DskipTests").WithCwd(projectPath)
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

func NewMavenCli(commandRunner exec.CommandRunner, projectPath string, rootProjectPath string) MavenCli {
	return &mavenCli{
		commandRunner:   commandRunner,
		projectPath:     projectPath,
		rootProjectPath: rootProjectPath,
	}
}
