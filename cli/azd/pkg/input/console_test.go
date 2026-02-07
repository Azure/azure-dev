// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

type lineCapturer struct {
	captured []string
}

func (l *lineCapturer) Write(bytes []byte) (n int, err error) {
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
	// We need to give it some time to paint.
	const waitTime = 50 * time.Millisecond

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

	ctx := context.Background()
	require.Len(t, lines.captured, 0)
	c.ShowSpinner(ctx, "Some title.", Step)

	time.Sleep(waitTime)
	require.Len(t, lines.captured, 1)
	require.Equal(t, lines.captured[0], "Some title.")

	c.ShowSpinner(ctx, "Some title 2.", Step)
	time.Sleep(waitTime)
	require.Len(t, lines.captured, 2)
	require.Equal(t, lines.captured[1], "Some title 2.")

	c.StopSpinner(ctx, "", StepDone)
	time.Sleep(waitTime)
	require.Len(t, lines.captured, 2)
	require.Equal(t, lines.captured[1], "Some title 2.")

	c.ShowSpinner(ctx, "Some title 3.", Step)
	time.Sleep(waitTime)
	require.Len(t, lines.captured, 3)
	require.Equal(t, lines.captured[2], "Some title 3.")

	c.Message(ctx, "Some message.")
	time.Sleep(waitTime)
	require.Len(t, lines.captured, 4)
	require.Equal(t, lines.captured[3], "Some message.")

	c.StopSpinner(ctx, "Done.", StepDone)
	time.Sleep(waitTime)
	require.Len(t, lines.captured, 5)
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

		res, err := c.Confirm(context.Background(), ConsoleOptions{Message: "Are you sure?", DefaultValue: true})
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

		res, err := c.Prompt(context.Background(), ConsoleOptions{Message: "What is your name?"})
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
			context.Background(),
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
			context.Background(),
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
			Value:  to.Ptr(res),
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

			c.Message(context.Background(), "hello world")

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
	c.Message(context.Background(), "hello world")

	got := buf.String()
	require.Contains(t, got, `"consoleMessage"`,
		"invalid query should fall back to full envelope")
}

// writerAdapter wraps *strings.Builder to satisfy io.Writer for test purposes.
type writerAdapter struct {
	*strings.Builder
}
