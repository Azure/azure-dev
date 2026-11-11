// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONPreviewPreservesContent(t *testing.T) {
	source := []byte(`{"large":9007199254740993,"decimal":1.234567890123456789,` +
		`"url":"https://example.com/a?q=x","text":"\u001b[31m","empty":null}`)
	for _, color := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "color"}[color], func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			var output bytes.Buffer
			require.NoError(t, RenderJSON(t.Context(), &output, source, color))
			assert.JSONEq(t, string(source), ansi.Strip(output.String()))
			assert.Contains(t, output.String(), "9007199254740993")
			assert.Contains(t, output.String(), "1.234567890123456789")
			assert.Contains(t, output.String(), `\u001b[31m`)
			assert.Equal(t, color, bytes.Contains(output.Bytes(), []byte("\x1b")))
		})
	}
	t.Setenv("NO_COLOR", "1")
	var output bytes.Buffer
	require.NoError(t, RenderJSON(t.Context(), &output, source, true))
	assert.NotContains(t, output.String(), "\x1b")
}

type jsonFailureWriter struct{}

func (jsonFailureWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestJSONPreviewFailures(t *testing.T) {
	var output bytes.Buffer
	require.ErrorContains(t, RenderJSON(t.Context(), &output, []byte(`{`), true), "format JSON")
	assert.Empty(t, output.String())
	require.ErrorContains(t, RenderJSON(t.Context(), jsonFailureWriter{}, []byte(`{}`), false), "output unavailable")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, RenderJSON(ctx, &output, []byte(`{}`), false), context.Canceled)
	assert.Empty(t, output.String())
}

func TestJSONPreviewEscapesTerminalControlsWithoutLosingValues(t *testing.T) {
	values := map[string]string{"controls": "\u009b31m\u009dtitle\u202evalue", "url": "https://example.com/a?q=x"}
	source, err := json.Marshal(values)
	require.NoError(t, err)
	for _, color := range []bool{false, true} {
		var output bytes.Buffer
		require.NoError(t, RenderJSON(t.Context(), &output, source, color))
		plain := ansi.Strip(output.String())
		var decoded map[string]string
		require.NoError(t, json.Unmarshal([]byte(plain), &decoded))
		assert.Equal(t, values, decoded)
		assert.NotContains(t, plain, "\u009b")
		assert.NotContains(t, plain, "\u009d")
		assert.NotContains(t, plain, "\u202e")
	}
}
