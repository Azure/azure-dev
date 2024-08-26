// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package sqlcmd

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// sqlCmdCliVersion is the minimum version of sqlCmd cli that we require
var sqlCmdCliVersion semver.Version = semver.MustParse("1.8.0")

func NewSqlCmdCli(ctx context.Context, console input.Console, commandRunner exec.CommandRunner) (*SqlCmdCli, error) {
	return newSqlCmdCliImplementation(ctx, console, commandRunner, http.DefaultClient, downloadSqlCmd, extractSqlCmdCli)
}

// NewSqlCmdCliImplementation is like NewSqlCmdCli but allows providing a custom transport to use when downloading the
// sqlCmd CLI, for testing purposes.
func newSqlCmdCliImplementation(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
	acquireSqlCmdCliImpl getSqlCmdCliImplementation,
	extractImplementation extractSqlCmdCliFromFileImplementation,
) (*SqlCmdCli, error) {
	if override := os.Getenv("AZD_SQL_CMD_CLI_TOOL_PATH"); override != "" {
		log.Printf("using external sqlCmd cli tool: %s", override)
		cli := &SqlCmdCli{
			path:          override,
			commandRunner: commandRunner,
		}
		cli.logVersion(ctx)

		return cli, nil
	}

	sqlCmdCliPath, err := azdSqlCmdCliPath()
	if err != nil {
		return nil, fmt.Errorf("getting sqlCmd cli default path: %w", err)
	}

	if _, err = os.Stat(sqlCmdCliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("getting file information from sqlCmd cli default path: %w", err)
	}
	var installSqlCmdCli bool
	if errors.Is(err, os.ErrNotExist) || !expectedVersionInstalled(ctx, commandRunner, sqlCmdCliPath) {
		installSqlCmdCli = true
	}
	if installSqlCmdCli {
		if err := os.MkdirAll(filepath.Dir(sqlCmdCliPath), osutil.PermissionDirectory); err != nil {
			return nil, fmt.Errorf("creating sqlCmd cli default path: %w", err)
		}

		msg := "setting up sqlCmd connection"
		console.ShowSpinner(ctx, msg, input.Step)
		err = acquireSqlCmdCliImpl(ctx, transporter, sqlCmdCliVersion, extractImplementation, sqlCmdCliPath)
		console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("setting up sqlCmd connection: %w", err)
		}
	}

	cli := &SqlCmdCli{
		path:          sqlCmdCliPath,
		commandRunner: commandRunner,
	}
	cli.logVersion(ctx)
	return cli, nil
}

func (cli *SqlCmdCli) logVersion(ctx context.Context) {
	if ver, err := cli.extractVersion(ctx); err == nil {
		log.Printf("sqlcmd cli version: %s", ver)
	} else {
		log.Printf("could not determine github cli version: %s", err)
	}
}

// extractVersion gets the version of the sqlCmd CLI, from the output of `sqlCmd --version`
func (cli *SqlCmdCli) extractVersion(ctx context.Context) (string, error) {
	runArgs := cli.newRunArgs("--version")
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("error running sqlcmd --version: %w", err)
	}
	return res.Stdout, nil
}

// azdSqlCmdCliPath returns the path where we store our local copy of sqlCmd cli ($AZD_CONFIG_DIR/bin).
func azdSqlCmdCliPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "bin", sqlCmdCliName()), nil
}

func sqlCmdCliName() string {
	if runtime.GOOS == "windows" {
		return "sqlcmd.exe"
	}
	return "sqlcmd"
}

type SqlCmdCli struct {
	commandRunner exec.CommandRunner
	path          string
}

func expectedVersionInstalled(ctx context.Context, commandRunner exec.CommandRunner, binaryPath string) bool {
	sqlCmdVersion, err := tools.ExecuteCommand(ctx, commandRunner, binaryPath, "--version")
	if err != nil {
		log.Printf("checking %s version: %s", sqlCmdToolName, err.Error())
		return false
	}
	sqlCmdSemver, err := tools.ExtractVersion(sqlCmdVersion)
	if err != nil {
		log.Printf("converting to semver version fails: %s", err.Error())
		return false
	}
	if sqlCmdSemver.LT(sqlCmdCliVersion) {
		log.Printf("Found sqlCmd cli version %s. Expected version: %s.", sqlCmdSemver.String(), sqlCmdCliVersion.String())
		return false
	}
	return true
}

const sqlCmdToolName = "sqlCmd CLI"

func (cli *SqlCmdCli) Name() string {
	return sqlCmdToolName
}

func (cli *SqlCmdCli) BinaryPath() string {
	return cli.path
}

func (cli *SqlCmdCli) InstallUrl() string {
	return "https://github.com/microsoft/go-sqlcmd"
}

func (cli *SqlCmdCli) newRunArgs(args ...string) exec.RunArgs {
	return exec.NewRunArgs(cli.path, args...)
}

func (cli *SqlCmdCli) run(ctx context.Context, runArgs exec.RunArgs) (exec.RunResult, error) {
	return cli.commandRunner.Run(ctx, runArgs)
}

func extractFromZip(src, dst string) (string, error) {
	zipReader, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}

	log.Printf("extract from zip %s", src)
	defer zipReader.Close()

	var extractedAt string
	for _, file := range zipReader.File {
		fileName := file.FileInfo().Name()
		if !file.FileInfo().IsDir() && fileName == sqlCmdCliName() {
			log.Printf("found cli at: %s", file.Name)
			fileReader, err := file.Open()
			if err != nil {
				return extractedAt, err
			}
			filePath := filepath.Join(dst, fileName)
			sqlCmdCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return extractedAt, err
			}
			defer sqlCmdCliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(sqlCmdCliFile, fileReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}
	if extractedAt != "" {
		log.Printf("extracted to: %s", extractedAt)
		return extractedAt, nil
	}
	return extractedAt, fmt.Errorf("sqlCmd cli binary was not found within the zip file")
}

func extractFromTar(src, dst string) (string, error) {
	bz2File, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer bz2File.Close()

	bz2Reader := bzip2.NewReader(bz2File)

	var extractedAt string
	// tarReader doesn't need to be closed as it is closed by the gz reader
	tarReader := tar.NewReader(bz2Reader)
	for {
		fileHeader, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return extractedAt, fmt.Errorf("did not find sqlcmd cli within tar file")
		}
		if fileHeader == nil {
			continue
		}
		if err != nil {
			return extractedAt, err
		}
		// Tha name contains the path, remove it
		fileNameParts := strings.Split(fileHeader.Name, "/")
		fileName := fileNameParts[len(fileNameParts)-1]
		// cspell: disable-next-line `Typeflag` is comming fron *tar.Header
		if fileHeader.Typeflag == tar.TypeReg && fileName == "sqlcmd" {
			filePath := filepath.Join(dst, fileName)
			sqlCmdCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(fileHeader.Mode))
			if err != nil {
				return extractedAt, err
			}
			defer sqlCmdCliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(sqlCmdCliFile, tarReader)
			if err != nil {
				return extractedAt, err
			}
			extractedAt = filePath
			break
		}
	}
	if extractedAt != "" {
		return extractedAt, nil
	}
	return extractedAt, fmt.Errorf("extract from tar error. Extraction ended in unexpected state.")
}

// extractSqlCmdCli gets the sqlCmd cli from either a zip or a tar.gz
func extractSqlCmdCli(src, dst string) (string, error) {
	if strings.HasSuffix(src, ".zip") {
		return extractFromZip(src, dst)
	} else if strings.HasSuffix(src, ".tar.bz2") {
		return extractFromTar(src, dst)
	}
	return "", fmt.Errorf("Unknown format while trying to extract")
}

// getSqlCmdCliImplementation defines the contract function to acquire the sqlCmd cli.
// The `outputPath` is the destination where the sqlCmd cli is place it.
type getSqlCmdCliImplementation func(
	ctx context.Context,
	transporter policy.Transporter,
	sqlCmdVersion semver.Version,
	extractImplementation extractSqlCmdCliFromFileImplementation,
	outputPath string) error

// extractSqlCmdCliFromFileImplementation defines how the cli is extracted
type extractSqlCmdCliFromFileImplementation func(src, dst string) (string, error)

// downloadSqlCmd downloads a given version of sqlCmd cli from the release site.
func downloadSqlCmd(
	ctx context.Context,
	transporter policy.Transporter,
	sqlCmdVersion semver.Version,
	extractImplementation extractSqlCmdCliFromFileImplementation,
	path string) error {

	binaryName := func(platform string) string {
		return fmt.Sprintf("sqlcmd-%s", platform)
	}

	systemArch := runtime.GOARCH
	// arm and x86 not supported (similar to bicep)
	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = binaryName(fmt.Sprintf("windows-%s.zip", systemArch))
	case "darwin":
		releaseName = binaryName(fmt.Sprintf("darwin-%s.tar.bz2", systemArch))
	case "linux":
		releaseName = binaryName(fmt.Sprintf("linux-%s.tar.bz2", systemArch))
	default:
		return fmt.Errorf("unsupported platform")
	}

	sqlCmdReleaseUrl := fmt.Sprintf(
		"https://github.com/microsoft/go-sqlcmd/releases/download/v%s/%s", sqlCmdVersion, releaseName)

	log.Printf("downloading sqlCmd cli release %s -> %s", sqlCmdReleaseUrl, releaseName)

	req, err := http.NewRequestWithContext(ctx, "GET", sqlCmdReleaseUrl, nil)
	if err != nil {
		return err
	}

	resp, err := transporter.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http error %d", resp.StatusCode)
	}

	tmpPath := filepath.Dir(path)
	compressedRelease, err := os.CreateTemp(tmpPath, releaseName)
	if err != nil {
		return err
	}
	defer func() {
		_ = compressedRelease.Close()
		_ = os.Remove(compressedRelease.Name())
	}()

	if _, err := io.Copy(compressedRelease, resp.Body); err != nil {
		return err
	}
	if err := compressedRelease.Close(); err != nil {
		return err
	}

	// change file name from temporal name to the final name, as the download has completed
	compressedFileName := filepath.Join(tmpPath, releaseName)
	if err := osutil.Rename(ctx, compressedRelease.Name(), compressedFileName); err != nil {
		return err
	}
	defer func() {
		log.Printf("delete %s", compressedFileName)
		_ = os.Remove(compressedFileName)
	}()

	// unzip downloaded file
	log.Printf("extracting file %s", compressedFileName)
	_, err = extractImplementation(compressedFileName, tmpPath)
	if err != nil {
		return err
	}

	return nil
}

func (cli *SqlCmdCli) ExecuteScript(ctx context.Context, server, dbName, path string, env []string) (string, error) {
	runArgs := cli.newRunArgs("-G", "-l", "30", "-S", server, "-d", dbName, "-i", path).WithEnv(env)
	res, err := cli.run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("error running sqlcmd: %w", err)
	}
	return res.Stdout, nil
}
