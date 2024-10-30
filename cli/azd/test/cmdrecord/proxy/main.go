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
	"github.com/braydonk/yaml"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
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

var errInteractionNotFound = errors.New("interaction not found")

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
				id, err := a.nextInteractionId()
				if err != nil {
					return fmt.Errorf("getting interaction number: %w", err)
				}

				return a.record(id)
			case recorder.ModeReplayOnly:
				interaction, err := a.loadInteraction(intercept.ArgsMatch)
				if err != nil {
					return err
				}
				return a.replay(interaction)
			case recorder.ModeReplayWithNewEpisodes:
				interaction, err := a.loadInteraction(intercept.ArgsMatch)
				if errors.Is(err, errInteractionNotFound) {
					return a.record(interaction.Id)
				} else if err != nil {
					return err
				}

				return a.replay(interaction)
			case recorder.ModePassthrough:
				return a.passthrough()
			default:
				panic(fmt.Sprintf("unsupported mode: %d", a.config.RecordMode))
			}
		}
	}

	// the default behavior is to pass through all interactions unless an intercept matches
	return a.passthrough()
}

func (a *App) record(id int) error {
	stdOut, err := os.Create(a.stdoutFile(id))
	if err != nil {
		return err
	}

	stdErr, err := os.Create(a.stderrFile(id))
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
	if errors.As(runErr, &exitError) && !exitError.Exited() {
		return errSignalTerm
	}

	recorded := cmdrecord.Interaction{Id: id}
	recorded.Args = os.Args[1:]
	recorded.ExitCode = cmd.ProcessState.ExitCode()
	contents, err := yaml.Marshal(recorded)
	if err != nil {
		return err
	}
	err = os.WriteFile(
		a.metaFile(id),
		contents,
		0600)
	if err != nil {
		return err
	}

	if cmd.ProcessState.ExitCode() != 0 {
		return ErrExitCode{cmd.ProcessState.ExitCode()}
	}
	return nil
}

func (a *App) loadInteraction(argsMatch string) (cmdrecord.Interaction, error) {
	recorded := cmdrecord.Interaction{}
	argsMatchRegexp := regexp.MustCompile(argsMatch)
	id, err := a.nextInteractionId()
	if err != nil {
		return cmdrecord.Interaction{}, fmt.Errorf("getting interaction number: %w", err)
	}

	recorded.Id = id
	content, err := os.ReadFile(a.metaFile(id))
	if errors.Is(err, os.ErrNotExist) {
		return recorded, fmt.Errorf("%w: args '%s'", errInteractionNotFound, strings.Join(os.Args[1:], " "))
	}
	if err != nil {
		return recorded, fmt.Errorf("getting meta file: %w", err)
	}

	err = yaml.Unmarshal(content, &recorded)
	if err != nil {
		return recorded, fmt.Errorf("unmarshalling interaction: %w", err)
	}

	if !argsMatchRegexp.MatchString(strings.Join(recorded.Args, " ")) {
		return recorded, fmt.Errorf(
			"%w: ArgsMatch '%s' does not match recorded args '%s'",
			errInteractionNotFound,
			argsMatch,
			strings.Join(recorded.Args, " "))
	}

	return recorded, nil
}

func (a *App) replay(interaction cmdrecord.Interaction) error {
	var stdOutCopyErr, stdErrCopyErr error
	stdOutCopyDone := make(chan (bool), 1)
	stdErrCopyDone := make(chan (bool), 1)
	go func() {
		file, err := os.Open(a.stdoutFile(interaction.Id))
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
		file, err := os.Open(a.stderrFile(interaction.Id))
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

	if interaction.ExitCode != 0 {
		return ErrExitCode{interaction.ExitCode}
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
	if errors.As(err, &exitError) && !exitError.Exited() {
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

func (a *App) nextInteractionId() (int, error) {
	err := os.MkdirAll(a.config.CassetteName, 0755)
	if err != nil {
		return -1, err
	}

	name := filepath.Join(a.config.CassetteName, cmdrecord.InteractionIdFile)
	contents, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, os.WriteFile(name, []byte(fmt.Sprint(0)), 0600)
	}

	if err != nil {
		return -1, err
	}

	currentId, err := strconv.Atoi(string(contents))
	if err != nil {
		return -1, err
	}

	newId := currentId + 1
	err = os.WriteFile(name, []byte(fmt.Sprint(newId)), 0600)
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
		return fmt.Errorf("unmarshalling %s: %w", cmdrecord.ProxyConfigName, err)
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
	} else if errors.As(err, &exitCodeErr) {
		os.Exit(exitCodeErr.ExitCode)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())

		os.Exit(1)
	}
}
