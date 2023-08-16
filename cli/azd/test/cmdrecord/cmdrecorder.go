// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cmdrecord simplifies testing by implementing recording/playing back functionality for interacting with
// command line tools.
package cmdrecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

const ProxyConfigName = "proxy.config"

var buildOnce sync.Once

type Recorder struct {
	opt Options

	cassetteFile string
	proxyDir     string
}

type Options struct {
	CmdName      string        `json:"cmdName"`
	CassetteName string        `json:"cassettePath"`
	Intercepts   []Intercept   `json:"intercepts"`
	RecordMode   recorder.Mode `json:"recordMode"`
}

type Intercept struct {
	ArgsMatch string `json:"argsMatch"`
}

func NewWithOptions(opt Options) *Recorder {
	return &Recorder{
		opt: opt,
	}
}

// Start sets up the proxy tool and returns the directory where the proxy is located.
//
// The proxy tool is named exactly the same as the CmdName set. proxyDir can either be prepended directly to PATH or
// invoked directly by joining the proxyDir with the specified CmdName.
func (r *Recorder) Start() (proxyDir string, err error) {
	proxyBinaryName := "proxy"
	cmdPath := getCmdPath()
	var buildErr error
	buildOnce.Do(func() {
		err := build(cmdPath, "-o", proxyBinaryName)
		if err != nil {
			buildErr = err
		}
	})

	if buildErr != nil {
		return "", buildErr
	}

	proxyDir, err = os.MkdirTemp("", "cmdr")
	if err != nil {
		return "", fmt.Errorf("creating temp dir for proxy: %w", err)
	}
	r.proxyDir = proxyDir

	// rename to the proxy name
	src := filepath.Join(cmdPath, proxyBinaryName)
	destFile := r.opt.CmdName
	if runtime.GOOS == "windows" {
		destFile = destFile + ".exe"
	}
	dest := filepath.Join(proxyDir, destFile)
	err = os.Link(src, dest)
	if err != nil {
		srcFile, err := os.Open(src)
		if err != nil {
			return "", fmt.Errorf("opening src: %w", err)
		}

		destFile, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			return "", fmt.Errorf("creating dest: %w", err)
		}

		_, err = io.Copy(destFile, srcFile)
		if err != nil {
			return "", fmt.Errorf("moving proxy: %w", err)
		}

		if err := srcFile.Close(); err != nil {
			return "", err
		}
		if err := destFile.Close(); err != nil {
			return "", err
		}
	}

	cassetteFile := fmt.Sprintf("%s.%s.yaml", r.opt.CassetteName, r.opt.CmdName)
	cassetteDir := filepath.Join(proxyDir, "cassette")
	r.cassetteFile = cassetteFile

	loadErr := expand(cassetteFile, cassetteDir)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return "", fmt.Errorf("expanding cassette: %w", loadErr)
	}

	mode := r.opt.RecordMode
	// determine actual record mode for RecordOnce
	if mode == recorder.ModeRecordOnce {
		if errors.Is(loadErr, os.ErrNotExist) {
			mode = recorder.ModeRecordOnly
		} else {
			mode = recorder.ModeReplayOnly
		}
	}

	// Create a config file for the proxy
	cmdOptions := r.opt
	cmdOptions.CassetteName = cassetteDir
	cmdOptions.RecordMode = mode
	content, err := json.Marshal(cmdOptions)
	if err != nil {
		panic(err)
	}

	err = os.WriteFile(filepath.Join(proxyDir, ProxyConfigName), content, 0600)
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", ProxyConfigName, err)
	}

	err = os.MkdirAll(cassetteDir, 0755)
	if err != nil {
		return "", fmt.Errorf("creating cassette dir: %w", err)
	}

	return proxyDir, nil
}

func (r *Recorder) Stop() error {
	if r.opt.RecordMode == recorder.ModePassthrough ||
		r.opt.RecordMode == recorder.ModeReplayOnly {
		return nil
	}

	return zip(
		r.cassetteFile,
		r.opt.CmdName,
		filepath.Join(r.proxyDir, "cassette"))
}

func getCmdPath() string {
	_, b, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(b), "proxy")
}

func build(pkgPath string, args ...string) error {
	cmd := exec.Command("go", "build")
	cmd.Dir = pkgPath
	cmd.Args = append(cmd.Args, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"failed to build %s in %s: %w:\n%s",
			strings.Join(cmd.Args, " "),
			cmd.Dir,
			err,
			output)
	}

	return nil
}
