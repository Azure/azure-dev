// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

// newTestConsole creates a non-interactive console for unit testing
// without requiring a real terminal.
func newTestConsole(
	t *testing.T,
	noPrompt bool,
	formatter output.Formatter,
) (*AskerConsole, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	c := NewConsole(
		noPrompt,
		false,
		Writers{Output: buf},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: buf,
		},
		formatter,
		nil,
	)
	return c.(*AskerConsole), buf
}

func TestGetStepResultFormat(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want SpinnerUxType
	}{
		{"NilError", nil, StepDone},
		{"NonNilError", os.ErrNotExist, StepFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, GetStepResultFormat(tt.err))
		})
	}
}

func TestIsUnformatted(t *testing.T) {
	tests := []struct {
		name      string
		formatter output.Formatter
		want      bool
	}{
		{
			name:      "NilFormatter",
			formatter: nil,
			want:      true,
		},
		{
			name:      "NoneFormatter",
			formatter: mustFormatter(t, string(output.NoneFormat)),
			want:      true,
		},
		{
			name:      "JsonFormatter",
			formatter: &output.JsonFormatter{},
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestConsole(t, false, tt.formatter)
			require.Equal(t, tt.want, c.IsUnformatted())
		})
	}
}

func mustFormatter(t *testing.T, name string) output.Formatter {
	t.Helper()
	f, err := output.NewFormatter(name)
	require.NoError(t, err)
	return f
}

func TestGetFormatter(t *testing.T) {
	formatter := &output.JsonFormatter{}
	c, _ := newTestConsole(t, false, formatter)
	require.Equal(t, formatter, c.GetFormatter())
}

func TestGetFormatter_Nil(t *testing.T) {
	c, _ := newTestConsole(t, false, nil)
	require.Nil(t, c.GetFormatter())
}

func TestSetWriter(t *testing.T) {
	c, defaultBuf := newTestConsole(t, false, nil)

	// Confirm initial writer is the default
	require.Equal(t, defaultBuf, c.GetWriter())

	// Set a custom writer
	custom := &bytes.Buffer{}
	c.SetWriter(custom)
	require.Equal(t, custom, c.GetWriter())

	// Reset to default
	c.SetWriter(nil)
	require.Equal(t, defaultBuf, c.GetWriter())
}

func TestHandles(t *testing.T) {
	c, _ := newTestConsole(t, false, nil)
	h := c.Handles()
	require.NotNil(t, h.Stdin)
	require.NotNil(t, h.Stdout)
	require.NotNil(t, h.Stderr)
}

func TestIsNoPromptMode(t *testing.T) {
	tests := []struct {
		name     string
		noPrompt bool
		want     bool
	}{
		{"Enabled", true, true},
		{"Disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestConsole(t, tt.noPrompt, nil)
			require.Equal(t, tt.want, c.IsNoPromptMode())
		})
	}
}

func TestIsSpinnerInteractive_NonTerminal(t *testing.T) {
	c, _ := newTestConsole(t, false, nil)
	// Non-terminal consoles should not have interactive spinners
	require.False(t, c.IsSpinnerInteractive())
}

func TestIsSpinnerRunning_InitiallyStopped(t *testing.T) {
	c, _ := newTestConsole(t, false, nil)
	require.False(t, c.IsSpinnerRunning(context.Background()))
}

func TestSupportsPromptDialog(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ExternalPromptConfiguration
		want bool
	}{
		{
			name: "NoExternalPrompt",
			cfg:  nil,
			want: false,
		},
		{
			name: "WithExternalPrompt",
			cfg: &ExternalPromptConfiguration{
				Endpoint: "http://localhost",
				Key:      "key",
			},
			want: true,
		},
		{
			name: "WithDialogDisabled",
			cfg: &ExternalPromptConfiguration{
				Endpoint:       "http://localhost",
				Key:            "key",
				NoPromptDialog: true,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConsole(
				false,
				false,
				Writers{Output: &bytes.Buffer{}},
				ConsoleHandles{
					Stderr: os.Stderr,
					Stdin:  os.Stdin,
					Stdout: &bytes.Buffer{},
				},
				nil,
				tt.cfg,
			)
			require.Equal(t, tt.want, c.SupportsPromptDialog())
		})
	}
}

func TestEnsureBlankLine(t *testing.T) {
	tests := []struct {
		name     string
		last2    [2]byte
		wantCall bool
	}{
		{
			name:     "AlreadyBlank",
			last2:    [2]byte{'\n', '\n'},
			wantCall: false,
		},
		{
			name:     "OneNewLine",
			last2:    [2]byte{'a', '\n'},
			wantCall: true,
		},
		{
			name:     "NoNewLine",
			last2:    [2]byte{'a', 'b'},
			wantCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := mustFormatter(t, string(output.NoneFormat))
			c, buf := newTestConsole(t, false, formatter)
			c.last2Byte = tt.last2
			before := buf.Len()
			c.EnsureBlankLine(context.Background())
			if tt.wantCall {
				require.Greater(t, buf.Len(), before,
					"expected output to be written")
			} else {
				require.Equal(t, before, buf.Len(),
					"expected no output when already blank")
			}
		})
	}
}

func TestUpdateLastBytes(t *testing.T) {
	tests := []struct {
		name    string
		initial [2]byte
		msg     string
		want    [2]byte
	}{
		{
			name:    "EmptyMessage",
			initial: [2]byte{'a', 'b'},
			msg:     "",
			want:    [2]byte{'a', 'b'},
		},
		{
			name:    "SingleChar",
			initial: [2]byte{'a', 'b'},
			msg:     "x",
			want:    [2]byte{'b', 'x'},
		},
		{
			name:    "TwoChars",
			initial: [2]byte{'a', 'b'},
			msg:     "xy",
			want:    [2]byte{'x', 'y'},
		},
		{
			name:    "LongMessage",
			initial: [2]byte{0, 0},
			msg:     "hello\n",
			want:    [2]byte{'o', '\n'},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestConsole(t, false, nil)
			c.last2Byte = tt.initial
			c.updateLastBytes(tt.msg)
			require.Equal(t, tt.want, c.last2Byte)
		})
	}
}

func TestSpinnerTerminalMode(t *testing.T) {
	// Non-terminal should not have TTY mode
	mode := spinnerTerminalMode(false)
	require.NotZero(t, mode)

	// Terminal mode should have TTY mode
	ttyMode := spinnerTerminalMode(true)
	require.NotZero(t, ttyMode)
	require.NotEqual(t, mode, ttyMode)
}

func TestSetIndentation(t *testing.T) {
	tests := []struct {
		name   string
		spaces int
		want   string
	}{
		{"Zero", 0, ""},
		{"Two", 2, "  "},
		{"Four", 4, "    "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, setIndentation(tt.spaces))
		})
	}
}
