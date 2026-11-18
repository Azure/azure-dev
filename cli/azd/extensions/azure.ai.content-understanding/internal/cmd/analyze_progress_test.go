// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalysisTarget(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input analysis.Input
		want  string
	}{
		{"file", analysis.Input{Path: filepath.Join("private", "invoice.pdf")}, "invoice.pdf"},
		{"controls", analysis.Input{Path: "invoice\x1b[31m\n\u202e.pdf"}, "invoice.pdf"},
		{"URL", analysis.Input{ //nolint:gosec // Deliberately fake credentials for nondisclosure coverage.
			URL: "https://user:password@example.com/invoice.pdf?sig=secret#fragment",
		}, "https://example.com/invoice.pdf"},
		{"empty query", analysis.Input{URL: "https://example.com/invoice.pdf?"}, "https://example.com/invoice.pdf"},
		{"invalid URL", analysis.Input{URL: "https://user:password@%zz?sig=secret"}, "[invalid URL]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, analysisTarget(tt.input))
		})
	}
}

func TestAnalysisProgressLifecycle(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buffer bytes.Buffer
	progress := newAnalysisProgress(t.Context(), &buffer, 100)
	for index, target := range []string{"a.pdf", "b.pdf"} {
		require.NoError(t, progress.update(index, analysis.Input{Path: target}, true))
	}
	require.Eventually(t, func() bool {
		progress.mu.Lock()
		defer progress.mu.Unlock()
		return progress.frame >= 4
	}, time.Second*3, time.Millisecond*10)
	require.NoError(t, progress.update(0, analysis.Input{}, false))
	require.NoError(t, progress.update(1, analysis.Input{}, false))
	require.NoError(t, progress.stop())
	output := buffer.String()
	assert.Contains(t, output, "Analyzing a.pdf...")
	assert.Contains(t, output, "Analyzing b.pdf...")
	assert.Contains(t, output, "| Analyzing")
	assert.Contains(t, output, "/ Analyzing")
	withoutCursor := strings.ReplaceAll(output, "\r\x1b[2K", "")
	assert.Equal(t, withoutCursor, ansi.Strip(withoutCursor))
	assert.True(t, strings.HasSuffix(output, "\r\x1b[2K"))
}

func TestAnalysisProgressParallelAndSafe(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buffer bytes.Buffer
	progress := newAnalysisProgress(t.Context(), &buffer, 100)
	var workers sync.WaitGroup
	for index := range 12 {
		workers.Go(func() {
			err := progress.update(index, analysis.Input{ //nolint:gosec // Fake credentials for nondisclosure coverage.
				URL: "https://user:password@example.com/invoice.pdf?sig=secret#fragment",
			}, true)
			assert.NoError(t, err)
			assert.NoError(t, progress.update(index, analysis.Input{}, false))
		})
	}
	workers.Wait()
	require.NoError(t, progress.stop())
	output := buffer.String()
	assert.GreaterOrEqual(t, strings.Count(output, "Analyzing https://example.com/invoice.pdf..."), 12)
	for _, secret := range []string{"user", "password", "sig=", "secret", "fragment"} {
		assert.NotContains(t, output, secret)
	}
	assert.True(t, bytes.HasSuffix(buffer.Bytes(), []byte("\r\x1b[2K")))
}

type progressErrorWriter struct{ err error }

func (w progressErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestAnalysisProgressWriteFailure(t *testing.T) {
	for _, want := range []error{io.ErrClosedPipe, io.ErrShortWrite} {
		t.Run(want.Error(), func(t *testing.T) {
			writerErr := want
			if errors.Is(want, io.ErrShortWrite) {
				writerErr = nil
			}
			progress := newAnalysisProgress(t.Context(), progressErrorWriter{writerErr}, 80)
			require.ErrorIs(t, progress.update(0, analysis.Input{Path: "invoice.pdf"}, true), want)
			require.ErrorIs(t, progress.ctx.Err(), context.Canceled)
			require.ErrorIs(t, progress.stop(), want)
		})
	}
}

func TestAnalysisProgressEmptyAndCancelled(t *testing.T) {
	var buffer bytes.Buffer
	progress := newAnalysisProgress(t.Context(), &buffer, 80)
	require.NoError(t, progress.stop())
	assert.Empty(t, buffer.String())

	ctx, cancel := context.WithCancel(t.Context())
	progress = newAnalysisProgress(ctx, &buffer, 80)
	require.NoError(t, progress.update(0, analysis.Input{Path: "invoice.pdf"}, true))
	cancel()
	require.NoError(t, progress.stop())
	assert.True(t, strings.HasSuffix(buffer.String(), "\r\x1b[2K"))
}

func TestAnalysisProgressColorAndWidth(t *testing.T) {
	setHelpColor(t, true)
	for _, noColor := range []string{"", "1"} {
		t.Run("NO_COLOR="+noColor, func(t *testing.T) {
			t.Setenv("NO_COLOR", noColor)
			var buffer bytes.Buffer
			progress := newAnalysisProgress(t.Context(), &buffer, 40)
			require.NoError(t, progress.update(0, analysis.Input{Path: strings.Repeat("x", 100) + ".pdf"}, true))
			require.NoError(t, progress.stop())
			output := strings.ReplaceAll(buffer.String(), "\r\x1b[2K", "")
			assert.LessOrEqual(t, ansi.StringWidth(output), 39)
			assert.Contains(t, ansi.Strip(output), "Analyzing")
			if noColor == "" {
				assert.Contains(t, output, "\x1b[94m")
			} else {
				assert.Equal(t, output, ansi.Strip(output))
			}
		})
	}
}

func TestAnalysisProgressRedirected(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer file.Close()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	defer read.Close()
	defer write.Close()
	var buffer bytes.Buffer
	for _, writer := range []io.Writer{file, write, &buffer} {
		for _, format := range []string{"default", "json", "none"} {
			called := false
			_, err := executeAnalysis(t.Context(), writer, format,
				func(_ context.Context, progress func(int, analysis.Input, bool) error) (analysis.Execution, error) {
					called = true
					assert.Nil(t, progress)
					return analysis.Execution{}, nil
				})
			require.NoError(t, err)
			assert.True(t, called)
		}
	}
	assert.Empty(t, buffer.String())
	info, err := file.Stat()
	require.NoError(t, err)
	assert.Zero(t, info.Size())
}

func TestAnalysisProgressTTY(t *testing.T) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("requires a controlling terminal")
	}
	defer terminal.Close()
	for _, format := range []string{"json", "none"} {
		_, enabled := progressTerminal(terminal, format)
		assert.False(t, enabled)
	}
	_, enabled := progressTerminal(terminal, "default")
	require.True(t, enabled)
	_, err = executeAnalysis(t.Context(), terminal, "default",
		func(_ context.Context, progress func(int, analysis.Input, bool) error) (analysis.Execution, error) {
			require.NotNil(t, progress)
			require.NoError(t, progress(0, analysis.Input{Path: "invoice.pdf"}, true))
			require.NoError(t, progress(0, analysis.Input{}, false))
			return analysis.Execution{}, nil
		})
	require.NoError(t, err)

	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint("command/remote=", remote), func(t *testing.T) {
			fixture := newAnalyzeFixture(t)
			directory := t.TempDir()
			input := filepath.Join(directory, "invoice.pdf")
			require.NoError(t, os.WriteFile(input, []byte("%PDF-1.7\n"), 0o600))
			if remote {
				input = "https://example.com/invoice.pdf?sig=private-signature#private-fragment"
			}
			fixture.client.call = func(
				_ context.Context, request cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				if remote {
					assert.Equal(t, input, request.Input.URL, "redaction must not change the service request")
				}
				return cuclient.AnalyzeResponse{JSON: json.RawMessage(analyzeEnvelope)}, nil
			}
			root := fixture.root()
			root.SetOut(terminal)
			root.SetErr(io.Discard)
			results := filepath.Join(directory, "results")
			report := filepath.Join(directory, "report.json")
			root.SetArgs([]string{"analyze", input, "--output-dir", results, "--report-file", report, "--no-prompt"})
			require.NoError(t, root.ExecuteContext(t.Context()))
			assert.EqualValues(t, 1, fixture.client.calls.Load())
			files, err := filepath.Glob(filepath.Join(results, "*"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			for _, path := range append(files, report) {
				body, err := os.ReadFile(path) //nolint:gosec // Paths are confined to this test's temporary directory.
				require.NoError(t, err)
				assert.NotContains(t, string(body), "Analyzing")
				assert.NotContains(t, string(body), "\x1b")
				assert.NotContains(t, string(body), "private-signature")
				assert.NotContains(t, string(body), "private-fragment")
			}
		})
	}
}
