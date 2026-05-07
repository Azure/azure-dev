// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

type lineCapturer struct {
	mu       sync.Mutex
	captured []string
}

func (l *lineCapturer) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.captured))
	copy(out, l.captured)
	return out
}

func (l *lineCapturer) Write(bytes []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sb strings.Builder
	for i, b := range bytes {
		if b == '\n' {
			l.captured = append(l.captured, sb.String())
			sb.Reset()
			continue
		}

		err = sb.WriteByte(b)
		if err != nil {
			return i, err
		}

	}
	return len(bytes), nil
}

// Verifies no extra output is printed in non-tty scenarios for the spinner.
func TestAskerConsole_Spinner_NonTty(t *testing.T) {
	// The underlying spinner relies on non-blocking channels for paint updates.
	// Poll until the expected output is painted instead of using fixed sleeps.
	const waitTimeout = 2 * time.Second
	const pollInterval = 5 * time.Millisecond

	formatter, err := output.NewFormatter(string(output.NoneFormat))
	require.NoError(t, err)

	lines := &lineCapturer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: lines},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: lines,
		},
		formatter,
		nil,
	)

	ctx := t.Context()
	require.Len(t, lines.lines(), 0)
	c.ShowSpinner(ctx, "Some title.", Step)

	require.Eventually(t, func() bool { return len(lines.lines()) == 1 }, waitTimeout, pollInterval)
	require.Equal(t, lines.lines()[0], "Some title.")

	c.ShowSpinner(ctx, "Some title 2.", Step)
	require.Eventually(t, func() bool { return len(lines.lines()) == 2 }, waitTimeout, pollInterval)
	require.Equal(t, lines.lines()[1], "Some title 2.")

	c.StopSpinner(ctx, "", StepDone)
	// StopSpinner with empty message should not add a new line; give the spinner a
	// chance to paint and assert count is still 2.
	require.Never(t, func() bool { return len(lines.lines()) != 2 }, 100*time.Millisecond, pollInterval)
	require.Equal(t, lines.lines()[1], "Some title 2.")

	c.ShowSpinner(ctx, "Some title 3.", Step)
	require.Eventually(t, func() bool { return len(lines.lines()) == 3 }, waitTimeout, pollInterval)
	require.Equal(t, lines.lines()[2], "Some title 3.")

	c.Message(ctx, "Some message.")
	require.Eventually(t, func() bool { return len(lines.lines()) == 4 }, waitTimeout, pollInterval)
	require.Equal(t, lines.lines()[3], "Some message.")

	c.StopSpinner(ctx, "Done.", StepDone)
	require.Eventually(t, func() bool { return len(lines.lines()) == 5 }, waitTimeout, pollInterval)
}

func TestAskerConsoleExternalPrompt(t *testing.T) {
	newConsole := func(externalPromptCfg *ExternalPromptConfiguration) Console {
		return NewConsole(
			false,
			false,
			Writers{
				Output: os.Stdout,
			},
			ConsoleHandles{
				Stderr: os.Stderr,
				Stdin:  os.Stdin,
				Stdout: os.Stdout,
			},
			nil,
			externalPromptCfg,
		)
	}

	t.Run("Confirm", func(t *testing.T) {
		server := newTestExternalPromptServer(func(body promptOptions) json.RawMessage {
			require.Equal(t, "confirm", body.Type)
			require.Equal(t, "Are you sure?", body.Options.Message)
			require.NotNil(t, body.Options.DefaultValue)
			require.True(t, (*body.Options.DefaultValue).(bool))

			return json.RawMessage(`"false"`)
		})
		t.Cleanup(server.Close)

		externalPromptCfg := &ExternalPromptConfiguration{
			Endpoint:    server.URL,
			Key:         "fake-key-for-testing",
			Transporter: http.DefaultClient,
		}

		c := newConsole(externalPromptCfg)

		res, err := c.Confirm(t.Context(), ConsoleOptions{Message: "Are you sure?", DefaultValue: true})
		require.NoError(t, err)
		require.False(t, res)
	})

	t.Run("Prompt", func(t *testing.T) {
		server := newTestExternalPromptServer(func(body promptOptions) json.RawMessage {
			require.Equal(t, "string", body.Type)
			require.Equal(t, "What is your name?", body.Options.Message)
			require.Nil(t, body.Options.DefaultValue)

			return json.RawMessage(`"John Doe"`)
		})
		t.Cleanup(server.Close)

		externalPromptCfg := &ExternalPromptConfiguration{
			Endpoint:    server.URL,
			Key:         "fake-key-for-testing",
			Transporter: http.DefaultClient,
		}

		c := newConsole(externalPromptCfg)

		res, err := c.Prompt(t.Context(), ConsoleOptions{Message: "What is your name?"})
		require.NoError(t, err)
		require.Equal(t, "John Doe", res)
	})

	t.Run("Select", func(t *testing.T) {
		server := newTestExternalPromptServer(func(body promptOptions) json.RawMessage {
			require.Equal(t, "select", body.Type)
			require.Equal(t, "What is your favorite color?", body.Options.Message)

			var choices []string
			var details []string

			for _, choice := range *body.Options.Choices {
				choices = append(choices, choice.Value)
				details = append(details, *choice.Detail)
			}

			require.Equal(t, []string{"Red", "Green", "Blue"}, choices)
			require.Equal(t, []string{"RedDetails", "GreenDetails", "BlueDetails"}, details)
			require.Nil(t, body.Options.DefaultValue)

			return json.RawMessage(`"Green"`)
		})
		t.Cleanup(server.Close)

		externalPromptCfg := &ExternalPromptConfiguration{
			Endpoint:    server.URL,
			Key:         "fake-key-for-testing",
			Transporter: http.DefaultClient,
		}

		c := newConsole(externalPromptCfg)

		res, err := c.Select(
			t.Context(),
			ConsoleOptions{
				Message:       "What is your favorite color?",
				Options:       []string{"Red", "Green", "Blue"},
				OptionDetails: []string{"RedDetails", "GreenDetails", "BlueDetails"},
			},
		)
		require.NoError(t, err)
		require.Equal(t, 1, res)
	})

	t.Run("MultiSelect", func(t *testing.T) {
		server := newTestExternalPromptServer(func(body promptOptions) json.RawMessage {
			require.Equal(t, "multiSelect", body.Type)
			require.Equal(t, "What are your favorite colors?", body.Options.Message)

			var choices []string
			var details []string

			for _, choice := range *body.Options.Choices {
				choices = append(choices, choice.Value)
				details = append(details, *choice.Detail)
			}

			require.Equal(t, []string{"Red", "Green", "Blue"}, choices)
			require.Equal(t, []string{"RedDetails", "GreenDetails", "BlueDetails"}, details)
			require.Nil(t, body.Options.DefaultValue)

			return json.RawMessage(`["Red", "Blue"]`)
		})
		t.Cleanup(server.Close)

		externalPromptCfg := &ExternalPromptConfiguration{
			Endpoint:    server.URL,
			Key:         "fake-key-for-testing",
			Transporter: http.DefaultClient,
		}

		c := newConsole(externalPromptCfg)

		res, err := c.MultiSelect(
			t.Context(),
			ConsoleOptions{
				Message:       "What are your favorite colors?",
				Options:       []string{"Red", "Green", "Blue"},
				OptionDetails: []string{"RedDetails", "GreenDetails", "BlueDetails"},
			},
		)
		require.NoError(t, err)
		require.Len(t, res, 2)
		require.Contains(t, res, "Red")
		require.Contains(t, res, "Blue")
	})
}

func newTestExternalPromptServer(handler func(promptOptions) json.RawMessage) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body promptOptions
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(err.Error()))
			return
		}

		res := handler(body)
		w.WriteHeader(http.StatusOK)

		respBody, _ := json.Marshal(promptResponse{
			Status: "success",
			Value:  &res,
		})

		_, _ = w.Write(respBody)
	}))
}

func TestAskerConsole_Message_JsonQueryFilter(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		assert func(t *testing.T, got string)
	}{
		{
			name:  "NoQuery",
			query: "",
			assert: func(t *testing.T, got string) {
				// Unmarshal into the full envelope and verify structure
				var env contracts.EventEnvelope
				err := json.Unmarshal([]byte(strings.TrimSpace(got)), &env)
				require.NoError(t, err, "output should be valid JSON envelope")
				require.Equal(t, contracts.ConsoleMessageEventDataType, env.Type)

				data, ok := env.Data.(map[string]any)
				require.True(t, ok, "Data should be a map, got %T", env.Data)
				// EventForMessage appends a trailing newline to the message
				require.Equal(t, "hello world\n", data["message"])
			},
		},
		{
			name:  "QueryDataMessage",
			query: "data.message",
			assert: func(t *testing.T, got string) {
				// Query should extract a bare JSON string, not an object
				var s string
				err := json.Unmarshal([]byte(strings.TrimSpace(got)), &s)
				require.NoError(t, err, "output should unmarshal as a JSON string")
				// EventForMessage appends a trailing newline to the message
				require.Equal(t, "hello world\n", s)
			},
		},
		{
			name:  "QueryType",
			query: "type",
			assert: func(t *testing.T, got string) {
				// Query should extract a bare JSON string, not an object
				var s string
				err := json.Unmarshal([]byte(strings.TrimSpace(got)), &s)
				require.NoError(t, err, "output should unmarshal as a JSON string")
				require.Equal(t, "consoleMessage", s)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &strings.Builder{}
			formatter := &output.JsonFormatter{Query: tc.query}

			c := NewConsole(
				true,
				false,
				Writers{Output: writerAdapter{buf}},
				ConsoleHandles{
					Stderr: os.Stderr,
					Stdin:  os.Stdin,
					Stdout: writerAdapter{buf},
				},
				formatter,
				nil,
			)

			c.Message(t.Context(), "hello world")

			tc.assert(t, buf.String())
		})
	}
}

func TestAskerConsole_Message_InvalidQuery_FallsBack(t *testing.T) {
	buf := &strings.Builder{}
	formatter := &output.JsonFormatter{Query: "[invalid"}

	c := NewConsole(
		true,
		false,
		Writers{Output: writerAdapter{buf}},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: writerAdapter{buf},
		},
		formatter,
		nil,
	)

	// Should not panic; falls back to unfiltered output
	c.Message(t.Context(), "hello world")

	got := buf.String()
	require.Contains(t, got, `"consoleMessage"`,
		"invalid query should fall back to full envelope")
}

func TestAskerConsole_Message_EmptySkippedInJson(t *testing.T) {
	buf := &strings.Builder{}
	formatter := &output.JsonFormatter{}

	c := NewConsole(
		true,
		false,
		Writers{Output: writerAdapter{buf}},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: writerAdapter{buf},
		},
		formatter,
		nil,
	)

	// An empty message should produce no JSON output (it's just a visual separator in text mode)
	c.Message(t.Context(), "")
	require.Empty(t, buf.String(), "empty message should not emit any JSON output")

	// A non-empty message should still produce JSON output
	c.Message(t.Context(), "hello")
	require.NotEmpty(t, buf.String(), "non-empty message should emit JSON output")
	require.Contains(t, buf.String(), `"consoleMessage"`)
}

// TestAskerConsole_Previewer_ConcurrentRefCount verifies that parallel callers of
// ShowPreviewer/StopPreviewer don't panic. This reproduces the bug where the execution graph executor
// runs deploy-web and deploy-api in parallel and the first StopPreviewer nils the shared
// previewer, causing the second writer to panic on Write.
func TestAskerConsole_Previewer_ConcurrentRefCount(t *testing.T) {
	formatter, err := output.NewFormatter(string(output.NoneFormat))
	require.NoError(t, err)

	lines := &lineCapturer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: lines},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: lines,
		},
		formatter,
		nil,
	)

	ctx := t.Context()

	// Simulate two concurrent graph steps both obtaining a previewer writer
	writerA := c.ShowPreviewer(ctx, &ShowPreviewerOptions{
		Prefix:       "  ",
		MaxLineCount: 8,
		Title:        "Deploy web",
	})
	writerB := c.ShowPreviewer(ctx, &ShowPreviewerOptions{
		Prefix:       "  ",
		MaxLineCount: 8,
		Title:        "Deploy api",
	})

	// Both writers should be usable
	_, err = writerA.Write([]byte("web: deploying...\n"))
	require.NoError(t, err)
	_, err = writerB.Write([]byte("api: deploying...\n"))
	require.NoError(t, err)

	// Step A finishes first and stops the previewer.
	// With ref-counting, the previewer should stay alive for step B.
	c.StopPreviewer(ctx, false)

	// Step B should still be able to write without panicking.
	_, err = writerB.Write([]byte("api: still deploying...\n"))
	require.NoError(t, err)

	// Step B finishes; this should actually tear down the previewer (refcount → 0).
	c.StopPreviewer(ctx, false)

	// A third StopPreviewer (no active users) should be a safe no-op.
	c.StopPreviewer(ctx, false)
}

// TestAskerConsole_Previewer_SingleUser verifies that the single-user path
// (no concurrency) still works correctly with the ref-counting changes.
func TestAskerConsole_Previewer_SingleUser(t *testing.T) {
	formatter, err := output.NewFormatter(string(output.NoneFormat))
	require.NoError(t, err)

	lines := &lineCapturer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: lines},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: lines,
		},
		formatter,
		nil,
	)

	ctx := t.Context()

	// Single user: show, write, stop — the original non-concurrent path
	writer := c.ShowPreviewer(ctx, nil)
	_, err = writer.Write([]byte("building container...\n"))
	require.NoError(t, err)

	c.StopPreviewer(ctx, false)

	// Writing after stop should not panic; it should be a graceful no-op
	n, err := writer.Write([]byte("late write\n"))
	require.NoError(t, err)
	require.Equal(t, len("late write\n"), n)
}

// TestAskerConsole_Previewer_ConcurrentWriteStress runs many goroutines writing
// and stopping concurrently to verify there are no data races.
func TestAskerConsole_Previewer_ConcurrentWriteStress(t *testing.T) {
	formatter, err := output.NewFormatter(string(output.NoneFormat))
	require.NoError(t, err)

	lines := &lineCapturer{}
	c := NewConsole(
		false,
		false,
		Writers{Output: lines},
		ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: lines,
		},
		formatter,
		nil,
	)

	ctx := t.Context()
	const numWriters = 10

	// All writers obtain their handle
	writers := make([]io.Writer, numWriters)
	for i := range numWriters {
		writers[i] = c.ShowPreviewer(ctx, nil)
	}

	// All write concurrently
	var wg sync.WaitGroup
	for i := range numWriters {
		wg.Go(func() {
			for j := range 20 {
				_, _ = writers[i].Write(
					fmt.Appendf(nil, "writer %d: msg %d\n", i, j),
				)
			}
		})
	}
	wg.Wait()

	// All stop (one at a time, but could also be concurrent)
	for range numWriters {
		c.StopPreviewer(ctx, false)
	}
}

// writerAdapter wraps *strings.Builder to satisfy io.Writer for test purposes.
type writerAdapter struct {
	*strings.Builder
}
