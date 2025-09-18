// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdrecord

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
)

// Verify that record + playback work together.
func TestRecordPlayback(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{
		opt: Options{
			CmdName:      "go",
			CassetteName: filepath.Join(dir, "go"),
			Intercepts: []Intercept{
				{"^version"},
				{"^help version"},
				{"^unknown command"},
			},
			RecordMode: recorder.ModeRecordOnly,
		},
	}

	// Record interactions
	proxyDir, err := r.Start()
	require.NoError(t, err)
	goProxy := filepath.Join(proxyDir, "go")

	output, err := runCmd(goProxy, "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help", "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "unknown", "command")
	require.Error(t, err, output)

	err = r.Stop()
	require.NoError(t, err)

	require.FileExists(t, r.cassetteFile)
	cstContent, err := os.ReadFile(r.cassetteFile)
	require.NoError(t, err)

	cst := Cassette{}
	err = yaml.Unmarshal(cstContent, &cst)
	require.NoError(t, err)

	require.Equal(t, "go", cst.ToolName)
	require.Equal(t, 3, len(cst.Interactions))

	// Playback interactions
	r.opt.RecordMode = recorder.ModeReplayOnly
	proxyDir, err = r.Start()
	require.NoError(t, err)
	goProxy = filepath.Join(proxyDir, "go")

	output, err = runCmd(goProxy, "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help", "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "unknown", "command")
	require.Error(t, err, output)

	err = r.Stop()
	require.NoError(t, err)

	// Playback with append interactions
	r.opt.RecordMode = recorder.ModeReplayWithNewEpisodes
	proxyDir, err = r.Start()
	require.NoError(t, err)
	goProxy = filepath.Join(proxyDir, "go")

	output, err = runCmd(goProxy, "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help", "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "unknown", "command")
	require.Error(t, err, output)

	output, err = runCmd(goProxy, "version")
	require.NoError(t, err, output)

	err = r.Stop()
	require.NoError(t, err)

	require.FileExists(t, r.cassetteFile)
	cstContent, err = os.ReadFile(r.cassetteFile)
	require.NoError(t, err)

	err = yaml.Unmarshal(cstContent, &cst)
	require.NoError(t, err)

	require.Equal(t, "go", cst.ToolName)
	require.Equal(t, 4, len(cst.Interactions))
}

// Verify schema compatibility -- does playback succeed from a known "frozen" cassette?
func TestPlaybackFromKnownFile(t *testing.T) {
	r := &Recorder{
		opt: Options{
			CmdName:      "go",
			CassetteName: filepath.Join("testdata", t.Name()),
			Intercepts: []Intercept{
				{"^version"},
				{"^help version"},
				{"^unknown command"},
			},
			RecordMode: recorder.ModeReplayOnly,
		},
	}

	content, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("%s.go.yaml", t.Name())))
	require.NoError(t, err)

	proxyDir, err := r.Start()
	require.NoError(t, err)

	goProxy := filepath.Join(proxyDir, "go")
	output, err := runCmd(goProxy, "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help", "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "unknown", "command")
	require.Error(t, err, output)

	err = r.Stop()
	require.NoError(t, err)

	afterContent, err := os.ReadFile(r.cassetteFile)
	require.NoError(t, err)
	require.Equal(t, string(content), string(afterContent), "cassette should remain unaltered")
}

func TestPassthrough(t *testing.T) {
	r := &Recorder{
		opt: Options{
			CmdName: "go",
			Intercepts: []Intercept{
				{"^version"},
				{"^help version"},
				{"^unknown command"},
			},
			RecordMode: recorder.ModePassthrough,
		},
	}

	proxyDir, err := r.Start()
	require.NoError(t, err)

	goProxy := filepath.Join(proxyDir, "go")
	output, err := runCmd(goProxy, "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "help", "version")
	require.NoError(t, err, output)

	output, err = runCmd(goProxy, "unknown", "command")
	require.Error(t, err, output)

	err = r.Stop()
	require.NoError(t, err)

	require.NoFileExists(t, r.opt.CassetteName)
}

func runCmd(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	return string(output), err
}
