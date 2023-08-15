package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/test/cmdrecord"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
	"gopkg.in/yaml.v3"
)

type ErrExitCode struct {
	ExitCode int
}

func (e ErrExitCode) Error() string {
	return fmt.Sprintf("exit with code: %d", e.ExitCode)
}

// Used internally to signal that the real command was terminated by a signal.
// In this case, the proxy command will also self-terminate using SIGKILL to be as close to observed behavior as possible.
var errSignalTerm = errors.New("terminated by signal")

type App struct {
	config cmdrecord.Options

	execDir string
}

func (a *App) Handle() error {
	for _, intercept := range a.config.Intercepts {
		r := regexp.MustCompile(intercept.ArgsMatch)
		if r.MatchString(strings.Join(os.Args[1:], " ")) {
			switch a.config.RecordMode {
			case recorder.ModeRecordOnly:
				return a.record()
			case recorder.ModeReplayOnly:
				return a.replay(intercept.ArgsMatch)
			case recorder.ModePassthrough:
				return a.passthrough()
			default:
				panic("unsupported mode")
			}
		}
	}

	// the default behavior is to pass through all interactions unless an intercept matches
	return a.passthrough()
}

func (a *App) record() error {
	interaction, err := a.getInteractionId()
	if err != nil {
		return fmt.Errorf("getting interaction number: %w", err)
	}

	stdOut, err := os.Create(a.stdoutFile(interaction))
	if err != nil {
		return err
	}

	stdErr, err := os.Create(a.stderrFile(interaction))
	if err != nil {
		return err
	}

	cmd, err := a.realCmd()
	if err != nil {
		return fmt.Errorf("getting real cmd: %w", err)
	}

	cmd.Stdout = io.MultiWriter(stdOut, os.Stdout)
	cmd.Stderr = io.MultiWriter(stdErr, os.Stderr)

	runErr := cmd.Run()
	if err := stdOut.Close(); err != nil {
		return err
	}
	if err := stdErr.Close(); err != nil {
		return err
	}
	var exitError *exec.ExitError
	if errors.Is(runErr, exitError) && !exitError.Exited() {
		return errSignalTerm
	}

	recorded := cmdrecord.Interaction{Id: interaction}
	recorded.Args = os.Args[1:]
	recorded.ExitCode = cmd.ProcessState.ExitCode()
	contents, err := yaml.Marshal(recorded)
	if err != nil {
		return err
	}
	err = os.WriteFile(
		a.metaFile(interaction),
		contents,
		0644)
	if err != nil {
		return err
	}

	if cmd.ProcessState.ExitCode() != 0 {
		return ErrExitCode{cmd.ProcessState.ExitCode()}
	}
	return nil
}

func (a *App) replay(argsMatch string) error {
	argsMatchRegexp := regexp.MustCompile(argsMatch)
	interaction, err := a.getInteractionId()
	if err != nil {
		return fmt.Errorf("getting interaction number: %w", err)
	}

	content, err := os.ReadFile(a.metaFile(interaction))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("interaction not found for '%s'", strings.Join(os.Args[1:], " "))
	}
	if err != nil {
		return fmt.Errorf("getting exit code: %w", err)
	}

	recorded := cmdrecord.Interaction{}
	err = yaml.Unmarshal(content, &recorded)
	if err != nil {
		return fmt.Errorf("unmarshaling interaction: %w", err)
	}

	if !argsMatchRegexp.MatchString(strings.Join(recorded.Args, " ")) {
		return fmt.Errorf(
			"interaction mismatch: ArgsMatch '%s' does not match recorded args '%s'",
			argsMatch,
			strings.Join(recorded.Args, " "))
	}

	var stdOutCopyErr, stdErrCopyErr error
	stdOutCopyDone := make(chan (bool), 1)
	stdErrCopyDone := make(chan (bool), 1)
	go func() {
		file, err := os.Open(a.stdoutFile(interaction))
		if err != nil {
			stdOutCopyErr = err
			return
		}
		_, err = io.Copy(os.Stdout, file)
		if err != nil {
			stdOutCopyErr = err
			return
		}
		stdOutCopyErr = file.Close()
		stdOutCopyDone <- true
	}()

	go func() {
		file, err := os.Open(a.stderrFile(interaction))
		if err != nil {
			stdErrCopyErr = err
			return
		}
		_, err = io.Copy(os.Stderr, file)
		if err != nil {
			stdErrCopyErr = err
			return
		}
		stdErrCopyErr = file.Close()
		stdErrCopyDone <- true
	}()

	<-stdOutCopyDone
	<-stdErrCopyDone
	if stdOutCopyErr != nil {
		return stdOutCopyErr
	}

	if stdErrCopyErr != nil {
		return stdErrCopyErr
	}

	if recorded.ExitCode != 0 {
		return ErrExitCode{recorded.ExitCode}
	}

	return nil
}

func (a *App) passthrough() error {
	cmd, err := a.realCmd()
	if err != nil {
		return fmt.Errorf("getting real cmd: %w", err)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	var exitError *exec.ExitError
	if errors.Is(err, exitError) && !exitError.Exited() {
		return errSignalTerm
	}

	if cmd.ProcessState.ExitCode() != 0 {
		return ErrExitCode{cmd.ProcessState.ExitCode()}
	}
	return nil
}

// realCmd returns the real command to run, with the proxy directory removed from the PATH environment variable.
// Stdin is attached to os.Stdin by default.
func (a *App) realCmd() (exec.Cmd, error) {
	err := removeEnvPathEntry(a.execDir)
	if err != nil {
		return exec.Cmd{}, fmt.Errorf("removing exec dir from PATH: %w", err)
	}

	path, err := exec.LookPath(a.config.CmdName)
	if err != nil {
		return exec.Cmd{}, err
	}

	if filepath.Dir(path) == a.execDir {
		panic("infinite recursion detected")
	}

	dir, err := os.Getwd()
	if err != nil {
		return exec.Cmd{}, err
	}

	return exec.Cmd{
		Path:  path,
		Args:  append([]string{path}, os.Args[1:]...),
		Stdin: os.Stdin,
		Dir:   dir,
	}, nil
}

// removeEnvPathEntry removes the given path entry, if present, from the PATH environment variable.
// It modifies the PATH environment variable in-place.
func removeEnvPathEntry(entry string) error {
	pathList := filepath.SplitList(os.Getenv("PATH"))
	for i, dir := range pathList {
		if dir == entry {
			pathList = append(pathList[:i], pathList[i+1:]...)
		}
	}

	err := os.Setenv("PATH", strings.Join(pathList, string(os.PathListSeparator)))
	if err != nil {
		return fmt.Errorf("setting new PATH: %w", err)
	}

	return nil
}

func (a *App) stdoutFile(interaction int) string {
	return filepath.Join(
		a.config.CassetteName,
		fmt.Sprintf("%s.%d.out", a.config.CmdName, interaction))
}

func (a *App) stderrFile(interaction int) string {
	return filepath.Join(
		a.config.CassetteName,
		fmt.Sprintf("%s.%d.err", a.config.CmdName, interaction))
}

func (a *App) metaFile(interaction int) string {
	return filepath.Join(
		a.config.CassetteName,
		fmt.Sprintf("%s.%d.meta", a.config.CmdName, interaction))
}

func (a *App) getInteractionId() (int, error) {
	err := os.MkdirAll(a.config.CassetteName, 0755)
	if err != nil {
		return -1, err
	}

	name := filepath.Join(a.config.CassetteName, cmdrecord.InteractionIdFile)
	contents, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, os.WriteFile(name, []byte(fmt.Sprint(0)), 0644)
	}

	if err != nil {
		return -1, err
	}

	currentId, err := strconv.Atoi(string(contents))
	if err != nil {
		return -1, err
	}

	newId := currentId + 1
	err = os.WriteFile(name, []byte(fmt.Sprint(newId)), 0644)
	return newId, err
}

func runMain() error {
	exec, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting current exec: %w", err)
	}

	execDir := filepath.Dir(exec)
	contents, err := os.ReadFile(filepath.Join(execDir, cmdrecord.ProxyConfigName))
	if err != nil {
		return fmt.Errorf("reading %s: %w", cmdrecord.ProxyConfigName, err)
	}

	config := cmdrecord.Options{}
	err = json.Unmarshal(contents, &config)
	if err != nil {
		return fmt.Errorf("unmarshaling %s: %w", cmdrecord.ProxyConfigName, err)
	}

	app := App{config: config, execDir: execDir}
	return app.Handle()
}

func main() {
	err := runMain()

	var exitCodeErr *ErrExitCode
	if errors.Is(err, errSignalTerm) {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			panic(err)
		}

		err = p.Kill()
		// The current process should stop at this point.
		// This should be unreachable, but in case anything happens, panic on err.
		panic(err)
	} else if errors.Is(err, exitCodeErr) {
		os.Exit(exitCodeErr.ExitCode)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())

		os.Exit(1)
	}
}
