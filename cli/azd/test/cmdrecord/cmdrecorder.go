package cmdrecord

import (
	"encoding/json"
	"fmt"
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
}

type Options struct {
	CmdName      string        `json:"cmdName"`
	CassettePath string        `json:"cassettePath"`
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

func (r *Recorder) Start() (string, error) {
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

	proxyDir, err := os.MkdirTemp("", "cmdr")
	if err != nil {
		return "", fmt.Errorf("creating temp dir for proxy: %w", err)
	}

	err = os.Rename(filepath.Join(cmdPath, proxyBinaryName), filepath.Join(proxyDir, r.opt.CmdName))
	if err != nil {
		return "", fmt.Errorf("moving proxy: %w", err)
	}

	content, err := json.Marshal(r.opt)
	if err != nil {
		panic(err)
	}

	err = os.WriteFile(filepath.Join(proxyDir, ProxyConfigName), content, 0644)
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", ProxyConfigName, err)
	}

	err = os.MkdirAll(r.opt.CassettePath, 0755)
	if err != nil {
		return "", fmt.Errorf("creating cassette dir: %w", err)
	}

	return proxyDir, nil
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
