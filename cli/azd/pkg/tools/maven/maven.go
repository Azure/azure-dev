package maven

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	osexec "os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type MavenCli interface {
	tools.ExternalTool
	SetPath(projectPath string, rootProjectPath string)
	ResolveDependencies(ctx context.Context, projectPath string) error
	Compile(ctx context.Context, projectPath string) error
	Package(ctx context.Context, projectPath string) error
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

func (m *mavenCli) CheckInstalled(ctx context.Context) error {
	_, err := m.mvnCmd()
	if err != nil {
		return err
	}

	if ver, err := m.extractVersion(ctx); err == nil {
		log.Printf("maven version: %s", ver)
	}

	return nil
}

func (m *mavenCli) SetPath(projectPath string, rootProjectPath string) {
	m.projectPath = projectPath
	m.rootProjectPath = rootProjectPath
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

// cMavenVersionRegexp captures the version number of maven from the output of "mvn --version"
//
// the output of mvn --version looks something like this:
// Apache Maven 3.9.1 (2e178502fcdbffc201671fb2537d0cb4b4cc58f8)
// Maven home: C:\Tools\apache-maven-3.9.1
// Java version: 17.0.6, vendor: Microsoft, runtime: C:\Program Files\Microsoft\jdk-17.0.6.10-hotspot
// Default locale: en_US, platform encoding: Cp1252
// OS name: "windows 11", version: "10.0", arch: "amd64", family: "windows"
var cMavenVersionRegexp = regexp.MustCompile(`Apache Maven (.*) \(`)

func (cli *mavenCli) extractVersion(ctx context.Context) (string, error) {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return "", err
	}

	runArgs := exec.NewRunArgs(mvnCmd, "--version")
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("failed to run %s --version: %w", mvnCmd, err)
	}

	parts := cMavenVersionRegexp.FindStringSubmatch(res.Stdout)
	if len(parts) != 2 {
		return "", fmt.Errorf("could not parse %s --version output, did not match expected format", mvnCmd)
	}

	return parts[1], nil
}

func (cli *mavenCli) Compile(ctx context.Context, projectPath string) error {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return err
	}

	runArgs := exec.NewRunArgs(mvnCmd, "compile").WithCwd(projectPath)
	_, err = cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn compile on project '%s' failed: %w", projectPath, err)
	}

	return nil
}

func (cli *mavenCli) Package(ctx context.Context, projectPath string) error {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return err
	}

	// Maven's package phase includes tests by default. Skip it explicitly.
	runArgs := exec.NewRunArgs(mvnCmd, "package", "-DskipTests").WithCwd(projectPath)
	_, err = cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn package on project '%s' failed: %w", projectPath, err)
	}

	return nil
}

func (cli *mavenCli) ResolveDependencies(ctx context.Context, projectPath string) error {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return err
	}
	runArgs := exec.NewRunArgs(mvnCmd, "dependency:resolve").WithCwd(projectPath)
	_, err = cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("mvn dependency:resolve on project '%s' failed: %w", projectPath, err)
	}

	return nil
}

func NewMavenCli(commandRunner exec.CommandRunner) MavenCli {
	return &mavenCli{
		commandRunner: commandRunner,
	}
}
