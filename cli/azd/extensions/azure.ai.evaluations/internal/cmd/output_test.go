// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The list commands are backed by two different services whose envelopes
// disagree — `data` on one side, `value` on the other. Emitting whichever one
// came back would make a caller's parsing depend on that accident, so every
// list emits a bare array instead.
func TestEmitJSONList_EmitsAnArrayNotAnEnvelope(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitJSONList(&buf, []string{"a", "b"}))
	assert.Equal(t, "[\n  \"a\",\n  \"b\"\n]\n", buf.String())
}

// A nil slice marshals to `null`, which a caller iterating the output cannot
// range over. An empty listing has to come back as an empty array.
func TestEmitJSONList_NilBecomesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	var none []string
	require.NoError(t, emitJSONList(&buf, none))
	assert.Equal(t, "[]\n", buf.String())
}

// ADO 5572140: every -o json failure must carry a "code", not only the
// handful of structured errors this extension classifies directly. An
// untyped error reaching failAs used to leave the field empty entirely.
func TestFailAsAlwaysReportsACode(t *testing.T) {
	t.Run("untyped error falls back rather than omitting code", func(t *testing.T) {
		cmd := jsonCmd(t, "json")
		var out bytes.Buffer
		cmd.SetOut(&out)
		priorExit := exitProcess
		exitCode := 0
		exitProcess = func(c int) { exitCode = c }
		t.Cleanup(func() { exitProcess = priorExit })

		cause := errors.New("an ordinary, unclassified failure")
		require.ErrorIs(t, failAs(cmd, cause), cause)
		assert.Equal(t, 1, exitCode)

		var doc jsonError
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
		assert.Equal(t, cause.Error(), doc.Error.Message)
		assert.Equal(t, unclassifiedErrorCode, doc.Error.Code,
			"an untyped error must not leave \"code\" empty or guessed from its message")
	})

	t.Run("a structured error keeps its own code, not the fallback", func(t *testing.T) {
		cmd := jsonCmd(t, "json")
		var out bytes.Buffer
		cmd.SetOut(&out)
		priorExit := exitProcess
		exitCode := 0
		exitProcess = func(c int) { exitCode = c }
		t.Cleanup(func() { exitProcess = priorExit })

		cause := exterrors.Validation(exterrors.CodeInvalidParameter, "a validation failure", "")
		require.ErrorIs(t, failAs(cmd, cause), cause)
		assert.Equal(t, 1, exitCode)

		var doc jsonError
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
		assert.Equal(t, exterrors.CodeInvalidParameter, doc.Error.Code)
	})
}

// errorCode must prefer the error's own structured code over the
// unclassified fallback, whichever structured type carries it.
func TestErrorCode_PrefersTheStructuredCodeOverTheFallback(t *testing.T) {
	assert.Equal(t, unclassifiedErrorCode, errorCode(errors.New("plain")))
	assert.Equal(t, exterrors.CodeInvalidParameter,
		errorCode(exterrors.Validation(exterrors.CodeInvalidParameter, "x", "")))
	assert.Equal(t, "ServiceRefused", errorCode(&azdext.ServiceError{
		Message: "x", ErrorCode: "ServiceRefused",
	}))
}

// `evaluator show --output-file` is pointed at a definition the developer is
// still working with, so a write that cannot complete must leave the old one
// intact rather than truncate it.
func TestWriteFileAtomic(t *testing.T) {
	t.Run("replaces an existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evaluator.json")
		require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
		require.NoError(t, writeFileAtomic(path, []byte("new")))

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(body))
	})

	t.Run("creates a file that was not there", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "evaluator.json")
		require.NoError(t, writeFileAtomic(path, []byte("new")))

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(body))
	})

	t.Run("a directory is refused, not removed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "evaluator.json")
		require.NoError(t, os.Mkdir(path, 0o750))

		require.Error(t, writeFileAtomic(path, []byte("new")))

		info, err := os.Stat(path)
		require.NoError(t, err, "the directory must survive")
		assert.True(t, info.IsDir())

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no temporary file may be left behind")
	})
}
