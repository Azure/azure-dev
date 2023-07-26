package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/test/recording/cmdrecord"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

const BadGateway = 5678

type ErrExitCode struct {
	ExitCode int
}

func (e ErrExitCode) Error() string {
	return fmt.Sprintf("exit with code: %d", e.ExitCode)
}

type App struct {
	config cmdrecord.Options
}

func (a *App) Handle() error {
	switch a.config.RecordMode {
	case recorder.ModeRecordOnly:
		interaction, err := getInteractionNumber()
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

		path, err := exec.LookPath(a.config.CmdName)
		if err != nil {
			return err
		}

		cmd := exec.Cmd{
			Path:   path,
			Args:   append([]string{path}, os.Args[1:]...),
			Stdin:  os.Stdin,
			Stdout: io.MultiWriter(os.Stdout, stdOut),
			Stderr: io.MultiWriter(os.Stderr, stdErr),
		}

		err = cmd.Run()
		if err := stdOut.Close(); err != nil {
			return err
		}
		if err := stdErr.Close(); err != nil {
			return err
		}
		var exitError *exec.ExitError
		if errors.Is(err, exitError) && !exitError.Exited() {
			return ErrExitCode{BadGateway}
		}

		err = os.WriteFile(
			a.exitCodeFile(interaction),
			[]byte(fmt.Sprint(cmd.ProcessState.ExitCode())),
			0644)
		if err != nil {
			return err
		}

		return nil
	case recorder.ModeReplayOnly:
		interaction, err := getInteractionNumber()
		if err != nil {
			return fmt.Errorf("getting interaction number: %w", err)
		}

		var stdOutErr, stdErrErr error
		stdOutDone := make(chan (bool), 1)
		stdErrDone := make(chan (bool), 1)
		go func() {
			file, err := os.Open(a.stdoutFile(interaction))
			if err != nil {
				stdOutErr = err
				return
			}
			_, err = io.Copy(os.Stdout, file)
			if err != nil {
				stdOutErr = err
				return
			}
			stdOutErr = file.Close()
			stdOutDone <- true
		}()

		go func() {
			file, err := os.Open(a.stderrFile(interaction))
			if err != nil {
				stdErrErr = err
				return
			}
			_, err = io.Copy(os.Stderr, file)
			if err != nil {
				stdErrErr = err
				return
			}
			stdErrErr = file.Close()
			stdErrDone <- true
		}()

		<-stdOutDone
		<-stdErrDone
		if stdOutErr != nil {
			return stdOutErr
		}

		if stdErrErr != nil {
			return stdErrErr
		}

		content, err := os.ReadFile(a.exitCodeFile(interaction))
		if err != nil {
			return fmt.Errorf("getting exit code: %w", err)
		}

		res, err := strconv.Atoi(string(content))
		if err != nil {
			return fmt.Errorf("getting exit code as int: %w", err)
		}

		return ErrExitCode{res}
	case recorder.ModePassthrough:
		cmd := exec.Cmd{
			Path:   a.config.CmdName,
			Args:   os.Args[1:],
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}

		err := cmd.Run()
		var exitError *exec.ExitError
		if errors.Is(err, exitError) && !exitError.Exited() {
			return ErrExitCode{BadGateway}
		}

		return nil
	default:
		panic("unsupported mode")
	}
}

func (a *App) stdoutFile(interaction int) string {
	return filepath.Join(
		a.config.CassettePath,
		fmt.Sprintf("%s.%d.out", a.config.CmdName, interaction))
}

func (a *App) stderrFile(interaction int) string {
	return filepath.Join(
		a.config.CassettePath,
		fmt.Sprintf("%s.%d.err", a.config.CmdName, interaction))
}

func (a *App) exitCodeFile(interaction int) string {
	return filepath.Join(
		a.config.CassettePath,
		fmt.Sprintf("%s.%d.exit", a.config.CmdName, interaction))
}

func getInteractionNumber() (int, error) {
	const name = "int-number.txt"
	contents, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return -1, err
	}

	res, err := strconv.Atoi(string(contents))
	if err != nil {
		return -1, err
	}

	err = os.WriteFile(name, []byte(fmt.Sprint(res+1)), 0644)
	return res, err
}

func runMain() error {
	exec, err := os.Executable()
	if err != nil {
		return fmt.Errorf("getting current exec: %w", err)
	}

	contents, err := os.ReadFile(filepath.Join(filepath.Dir(exec), cmdrecord.ProxyConfigName))
	if err != nil {
		return fmt.Errorf("reading %s: %w", cmdrecord.ProxyConfigName, err)
	}

	config := cmdrecord.Options{}
	err = json.Unmarshal(contents, &config)
	if err != nil {
		return fmt.Errorf("unmarshaling %s: %w", cmdrecord.ProxyConfigName, err)
	}

	app := App{config: config}
	return app.Handle()
}

func main() {
	err := runMain()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
	}

	var exitCodeErr *ErrExitCode
	if errors.Is(err, exitCodeErr) {
		os.Exit(exitCodeErr.ExitCode)
	}
	if err != nil {
		os.Exit(1)
	}
}
