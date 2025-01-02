package maven

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	osexec "os/exec"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

var _ tools.ExternalTool = (*Cli)(nil)

type Cli struct {
	commandRunner   exec.CommandRunner
	projectPath     string
	rootProjectPath string

	// Lazily initialized. Access through mvnCmd.
	mvnCmdStr  string
	mvnCmdOnce sync.Once
	mvnCmdErr  error
}

func (m *Cli) Name() string {
	return "Maven"
}

func (m *Cli) InstallUrl() string {
	return "https://maven.apache.org"
}

func (m *Cli) CheckInstalled(ctx context.Context) error {
	_, err := m.mvnCmd()
	if err != nil {
		return err
	}

	if ver, err := m.extractVersion(ctx); err == nil {
		log.Printf("maven version: %s", ver)
	}

	return nil
}

func (m *Cli) SetPath(projectPath string, rootProjectPath string) {
	m.projectPath = projectPath
	m.rootProjectPath = rootProjectPath
}

func (m *Cli) mvnCmd() (string, error) {
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

const downloadedMavenVersion = "3.9.9"

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

	return getDownloadedMvnCommand(downloadedMavenVersion)
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

		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, osexec.ErrNotFound) {
			return "", err
		}

		searchDir = filepath.Dir(searchDir)

		// Past root, terminate search and return not found
		if len(searchDir) < len(root) {
			return "", nil
		}
	}
}

// mavenVersionRegexp captures the version number of maven from the output of "mvn --version"
//
// the output of mvn --version looks something like this:
// Apache Maven 3.9.1 (2e178502fcdbffc201671fb2537d0cb4b4cc58f8)
// Maven home: C:\Tools\apache-maven-3.9.1
// Java version: 17.0.6, vendor: Microsoft, runtime: C:\Program Files\Microsoft\jdk-17.0.6.10-hotspot
// Default locale: en_US, platform encoding: Cp1252
// OS name: "windows 11", version: "10.0", arch: "amd64", family: "windows"
var mavenVersionRegexp = regexp.MustCompile(`Apache Maven (.*) \(`)

func (cli *Cli) extractVersion(ctx context.Context) (string, error) {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return "", err
	}

	runArgs := exec.NewRunArgs(mvnCmd, "--version")
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("failed to run %s --version: %w", mvnCmd, err)
	}

	parts := mavenVersionRegexp.FindStringSubmatch(res.Stdout)
	if len(parts) != 2 {
		return "", fmt.Errorf("could not parse %s --version output, did not match expected format", mvnCmd)
	}

	return parts[1], nil
}

func (cli *Cli) Compile(ctx context.Context, projectPath string) error {
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

func (cli *Cli) Package(ctx context.Context, projectPath string) error {
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

func (cli *Cli) ResolveDependencies(ctx context.Context, projectPath string) error {
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

var ErrPropertyNotFound = errors.New("property not found")

func (cli *Cli) GetProperty(ctx context.Context, propertyPath string, projectPath string) (string, error) {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return "", err
	}
	runArgs := exec.NewRunArgs(mvnCmd,
		"help:evaluate",
		// cspell: disable-next-line Dexpression and DforceStdout are maven command line arguments
		"-Dexpression="+propertyPath, "-q", "-DforceStdout").WithCwd(projectPath)
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("mvn help:evaluate on project '%s' failed: %w", projectPath, err)
	}

	result := strings.TrimSpace(res.Stdout)
	if result == "null object or invalid expression" {
		return "", ErrPropertyNotFound
	}

	return result, nil
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (cli *Cli) EffectivePom(ctx context.Context, pomPath string) (string, error) {
	mvnCmd, err := cli.mvnCmd()
	if err != nil {
		return "", err
	}
	pomDir := filepath.Dir(pomPath)
	runArgs := exec.NewRunArgs(mvnCmd, "help:effective-pom", "-f", pomPath).WithCwd(pomDir)
	result, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("failed to run mvn help:effective-pom for pom file: %s. error = %w", pomPath, err)
	}

	return getEffectivePomFromConsoleOutput(result.Stdout)
}

func getEffectivePomFromConsoleOutput(consoleOutput string) (string, error) {
	var effectivePom strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(consoleOutput))
	inProject := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "<project") {
			inProject = true
		} else if strings.HasPrefix(strings.TrimSpace(line), "</project>") {
			effectivePom.WriteString(line)
			break
		}
		if inProject {
			effectivePom.WriteString(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan console output. %w", err)
	}
	return effectivePom.String(), nil
}

func getDownloadedMvnCommand(mavenVersion string) (string, error) {
	mavenCommand, err := getAzdMvnCommand(mavenVersion)
	if err != nil {
		return "", err
	}
	if fileExists(mavenCommand) {
		log.Println("Skip downloading maven because it already exists.")
		return mavenCommand, nil
	}
	log.Println("Downloading maven")
	mavenDir, err := getAzdMvnDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(mavenDir); os.IsNotExist(err) {
		err = os.MkdirAll(mavenDir, os.ModePerm)
		if err != nil {
			return "", fmt.Errorf("unable to create directory: %w", err)
		}
	}

	mavenZipFilePath := filepath.Join(mavenDir, mavenZipFileName(mavenVersion))
	err = downloadMaven(mavenVersion, mavenZipFilePath)
	if err != nil {
		return "", err
	}
	err = unzip(mavenZipFilePath, mavenDir)
	if err != nil {
		return "", fmt.Errorf("failed to unzip maven bin.zip: %w", err)
	}
	return mavenCommand, nil
}

func getAzdMvnDir() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to get user home directory: %w", err)
	}
	return filepath.Join(userHome, ".azd", "java", "maven"), nil
}

func getAzdMvnCommand(mavenVersion string) (string, error) {
	mavenDir, err := getAzdMvnDir()
	if err != nil {
		return "", err
	}
	azdMvnCommand := filepath.Join(mavenDir, "apache-maven-"+mavenVersion, "bin", "mvn")
	return azdMvnCommand, nil
}

func mavenZipFileName(mavenVersion string) string {
	return "apache-maven-" + mavenVersion + "-bin.zip"
}

func mavenUrl(mavenVersion string) string {
	return "https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/" +
		mavenVersion + "/" + mavenZipFileName(mavenVersion)
}

func downloadMaven(mavenVersion string, filePath string) error {
	requestUrl := mavenUrl(mavenVersion)
	data, err := internal.Download(requestUrl)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0600)
}

func unzip(src string, destinationFolder string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func(reader *zip.ReadCloser) {
		err := reader.Close()
		if err != nil {
			log.Println("failed to close ReadCloser. %w", err)
		}
	}(reader)

	for _, file := range reader.File {
		destinationPath, err := getValidDestPath(destinationFolder, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			err := os.MkdirAll(destinationPath, os.ModePerm)
			if err != nil {
				return err
			}
		} else {
			if err = os.MkdirAll(filepath.Dir(destinationPath), os.ModePerm); err != nil {
				return err
			}

			outFile, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return err
			}
			defer func(outFile *os.File) {
				err := outFile.Close()
				if err != nil {
					log.Println("failed to close file. %w", err)
				}
			}(outFile)

			rc, err := file.Open()
			if err != nil {
				return err
			}
			defer func(rc io.ReadCloser) {
				err := rc.Close()
				if err != nil {
					log.Println("failed to close file. %w", err)
				}
			}(rc)

			for {
				_, err = io.CopyN(outFile, rc, 1_000_000)
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
			}
		}
	}
	return nil
}

func getValidDestPath(destinationFolder string, fileName string) (string, error) {
	destinationPath := filepath.Clean(filepath.Join(destinationFolder, fileName))
	if !strings.HasPrefix(destinationPath, destinationFolder+string(os.PathSeparator)) {
		return "", fmt.Errorf("%s: illegal file path", fileName)
	}
	return destinationPath, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	} else {
		return false
	}
}
