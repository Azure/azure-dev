package pack

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// PackVersion is the minimum version of pack that we require (and the one we fetch when we fetch pack on behalf of a
// user).
var PackVersion semver.Version = semver.MustParse("0.30.0")

var statusCodeFailureRegexp = regexp.MustCompile(`failed with status code: (\d+)`)

// All buildpacks groups have failed to detect w/o error.
// See https://buildpacks.io/docs/concepts/components/lifecycle/detect/#exit-codes
const StatusCodeUndetectedNoError = 20

// StatusCodeError is a status code error provided by pack CLI.
type StatusCodeError struct {
	Err error

	// See all available status codes https://buildpacks.io/docs/concepts/components/lifecycle/create/
	Code int
}

func (s *StatusCodeError) Error() string {
	return s.Err.Error()
}

func (s *StatusCodeError) Unwrap() error {
	return s.Err
}

type PackCli interface {
	Build(
		ctx context.Context,
		cwd string,
		builder string,
		imageName string,
		environ []string,
		progressWriter io.Writer,
	) error
}

// NewPackCli creates a new PackCli. azd manages its own copy of the pack CLI, stored in `$AZD_CONFIG_DIR/bin`. If
// pack is not present at this location, or if it is present but is older than the minimum supported version, it is
// downloaded.
func NewPackCli(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
) (PackCli, error) {
	return newPackCliImpl(
		ctx,
		console,
		commandRunner,
		http.DefaultClient,
		extractCli)
}

func NewPackCliWithPath(
	commandRunner exec.CommandRunner,
	cliPath string,
) PackCli {
	return &packCli{
		path:   cliPath,
		runner: commandRunner,
	}
}

// packCliPath returns the path where we store our local copy of pack ($AZD_CONFIG_DIR/bin).
func packCliPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(configDir, "bin", "pack.exe"), nil
	}

	return filepath.Join(configDir, "bin", "pack"), nil
}

func newPackCliImpl(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
	extract func(string, string) (string, error)) (PackCli, error) {
	if override := os.Getenv("AZD_PACK_TOOL_PATH"); override != "" {
		log.Printf("using external pack tool: %s", override)

		return &packCli{
			path:   override,
			runner: commandRunner,
		}, nil
	}

	cliPath, err := packCliPath()
	if err != nil {
		return nil, fmt.Errorf("finding pack: %w", err)
	}
	if _, err = os.Stat(cliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("finding pack: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(cliPath), osutil.PermissionDirectory); err != nil {
			return nil, fmt.Errorf("downloading pack: %w", err)
		}

		msg := "Acquiring pack cli"
		console.ShowSpinner(ctx, msg, input.Step)
		err := downloadPack(ctx, transporter, PackVersion, extract, cliPath)
		console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("downloading pack: %w", err)
		}
	}

	cli := &packCli{
		path:   cliPath,
		runner: commandRunner,
	}

	ver, err := cli.version(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking pack version: %w", err)
	}

	log.Printf("pack version: %s", ver)

	if ver.LT(PackVersion) {
		log.Printf("installed pack version %s is older than %s; updating.", ver.String(), PackVersion.String())

		msg := "Upgrading pack"
		console.ShowSpinner(ctx, msg, input.Step)
		err := downloadPack(ctx, transporter, PackVersion, extract, cliPath)
		console.StopSpinner(ctx, "", input.Step)
		if err != nil {
			return nil, fmt.Errorf("upgrading pack: %w", err)
		}
	}

	log.Printf("using local pack: %s", cliPath)

	return cli, nil
}

type packCli struct {
	path   string
	runner exec.CommandRunner
}

func (cli *packCli) version(ctx context.Context) (semver.Version, error) {
	packRes, err := cli.runner.Run(ctx, exec.NewRunArgs(cli.path, "--version"))
	if err != nil {
		return semver.Version{}, err
	}

	version, err := tools.ExtractVersion(packRes.Stdout)
	if err != nil {
		return semver.Version{}, err
	}

	return version, nil
}

func (cli *packCli) enableExperimental(ctx context.Context) error {
	runArgs := exec.NewRunArgs(cli.path, "config", "experimental", "true")
	runArgs.Interactive = false
	_, err := cli.runner.Run(ctx, runArgs)
	if err != nil {
		return err
	}

	return nil
}

func (cli *packCli) Build(
	ctx context.Context,
	cwd string,
	builder string,
	imageName string,
	environ []string,
	progressWriter io.Writer,
) error {
	err := cli.enableExperimental(ctx)
	if err != nil {
		return err
	}

	envArgs := make([]string, 0, 2*len(environ))
	for _, e := range environ {
		envArgs = append(envArgs, "--env", e)
	}

	runArgs := exec.NewRunArgs(cli.path, "build", imageName, "--builder", builder, "--path", cwd)
	runArgs.Args = append(runArgs.Args, envArgs...)
	if progressWriter != nil {
		runArgs = runArgs.WithStdOut(progressWriter).WithStdErr(progressWriter)
	}

	res, err := cli.runner.Run(ctx, runArgs)
	if err != nil {
		return wrapStatusCodeErr(err, res)
	}

	return nil
}

func wrapStatusCodeErr(err error, res exec.RunResult) error {
	if err == nil {
		return err
	}

	matches := statusCodeFailureRegexp.FindStringSubmatch(res.Stderr)
	if len(matches) == 2 {
		code, parseErr := strconv.Atoi(matches[1])
		if parseErr == nil {
			return &StatusCodeError{
				Err:  err,
				Code: code,
			}
		}
	}

	return err
}

func packName() string {
	if runtime.GOOS == "windows" {
		return "pack.exe"
	} else {
		return "pack"
	}
}

func extractFromZip(
	zipped string,
	out string) (string, error) {
	zipReader, err := zip.OpenReader(zipped)
	if err != nil {
		return "", err
	}

	log.Printf("extract from %s", zipped)
	defer zipReader.Close()

	var extractedAt string
	for _, file := range zipReader.File {
		fileName := file.FileInfo().Name()
		if !file.FileInfo().IsDir() && fileName == packName() {
			log.Printf("found cli at: %s", file.Name)
			fileReader, err := file.Open()
			if err != nil {
				return extractedAt, err
			}
			filePath := filepath.Join(out, fileName)
			cliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return extractedAt, err
			}
			defer cliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(cliFile, fileReader)
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
	return extractedAt, fmt.Errorf("pack cli binary was not found within the zip file")
}
func extractFromTar(
	zipped string,
	out string) (string, error) {
	gzFile, err := os.Open(zipped)
	if err != nil {
		return "", err
	}
	defer gzFile.Close()

	gzReader, err := gzip.NewReader(gzFile)
	if err != nil {
		return "", err
	}
	defer gzReader.Close()

	var extractedAt string
	// tarReader doesn't need to be closed as it is closed by the gz reader
	tarReader := tar.NewReader(gzReader)
	for {
		fileHeader, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return extractedAt, fmt.Errorf("did not find pack cli within tar file")
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
		if fileHeader.Typeflag == tar.TypeReg && fileName == "pack" {
			filePath := filepath.Join(out, fileName)
			packCliFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(fileHeader.Mode))
			if err != nil {
				return extractedAt, err
			}
			defer packCliFile.Close()
			/* #nosec G110 - decompression bomb false positive */
			_, err = io.Copy(packCliFile, tarReader)
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
	return extractedAt, fmt.Errorf("unable to find pack cli within archive")
}

// extractCli gets the pack cli from either a zip or a tar.gz
func extractCli(src, dst string) (string, error) {
	if strings.HasSuffix(src, ".zip") {
		return extractFromZip(src, dst)
	} else if strings.HasSuffix(src, ".tgz") {
		return extractFromTar(src, dst)
	}
	return "", fmt.Errorf("unknown format while trying to extract")
}

// downloadPack downloads a given version of pack cli from the release site.
func downloadPack(
	ctx context.Context,
	transporter policy.Transporter,
	version semver.Version,
	extractFile func(src, dst string) (string, error),
	path string) error {
	systemArch := runtime.GOARCH
	archString := "" // amd64 is the implicit default
	if systemArch != "amd64" {
		archString = fmt.Sprintf("-%s", systemArch)
	}

	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = fmt.Sprintf("pack-v%s-windows%s.zip", version, archString)
	case "darwin":
		releaseName = fmt.Sprintf("pack-v%s-macos%s.tgz", version, archString)
	case "linux":
		releaseName = fmt.Sprintf("pack-v%s-linux%s.tgz", version, archString)
	default:
		return fmt.Errorf("unsupported platform")
	}

	// example: https://github.com/buildpacks/pack/releases/download/v0.29.0/pack-v0.29.0-windows.zip
	ghReleaseUrl := fmt.Sprintf("https://github.com/buildpacks/pack/releases/download/v%s/%s", version, releaseName)
	log.Printf("downloading pack cli release %s -> %s", ghReleaseUrl, releaseName)

	spanCtx, span := tracing.Start(ctx, events.PackCliInstallEvent)
	defer span.End()

	req, err := http.NewRequestWithContext(spanCtx, "GET", ghReleaseUrl, nil)
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
	_, err = extractFile(compressedFileName, tmpPath)
	if err != nil {
		return err
	}

	return nil
}
