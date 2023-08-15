package cmdrecord

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v3/recorder"
	"gopkg.in/yaml.v3"
)

// Verify that record + playback work together.
func TestRecorder_RecordPlayback(t *testing.T) {
	dir := t.TempDir()
	r := &Recorder{
		opt: Options{
			CmdName:      "go",
			CassettePath: filepath.Join(dir, "go.yaml"),
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

	require.FileExists(t, r.opt.CassettePath)
	cstContent, err := os.ReadFile(r.opt.CassettePath)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join("testdata", "go.yaml"), cstContent, 0644)
	require.NoError(t, err)

	cst := Cassette{}
	err = yaml.Unmarshal(cstContent, &cst)
	require.NoError(t, err)

	require.Equal(t, "go", cst.ToolName)
	require.Equal(t, 3, len(cst.Interactions))

	// Playback interactions
	r.opt.RecordMode = recorder.ModeReplayOnly

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
}

// Verify schema compatibility -- does playback succeed from a known "frozen" cassette?
func TestRecorder_PlaybackFromKnownFile(t *testing.T) {
	r := &Recorder{
		opt: Options{
			CmdName:      "go",
			CassettePath: filepath.Join("testdata", "go.yaml"),
			Intercepts: []Intercept{
				{"^version"},
				{"^help version"},
				{"^unknown command"},
			},
			RecordMode: recorder.ModeReplayOnly,
		},
	}

	content, err := os.ReadFile(r.opt.CassettePath)
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

	afterContent, err := os.ReadFile(r.opt.CassettePath)
	require.NoError(t, err)
	require.Equal(t, string(content), string(afterContent), "cassette should remain unaltered")
}

func TestRecorder_Passthrough(t *testing.T) {
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

	require.NoFileExists(t, r.opt.CassettePath)
}

func runCmd(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	return string(output), err
}
