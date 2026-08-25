// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeCommandBackgroundFlagRegistered(t *testing.T) {
	t.Parallel()

	flag := newInvokeCommand(nil).Flags().Lookup("background")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

type orderingResponseStore struct {
	saved   bool
	saveErr error
}

func (s *orderingResponseStore) Get(context.Context, string) (*savedBackgroundResponse, error) {
	return nil, nil
}

func (s *orderingResponseStore) Save(context.Context, string, savedBackgroundResponse) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = true
	return nil
}

func (s *orderingResponseStore) Delete(context.Context, string) error {
	return nil
}

type afterSaveWriter struct {
	store  *orderingResponseStore
	output strings.Builder
}

func (w *afterSaveWriter) Write(p []byte) (int, error) {
	if !w.store.saved {
		return 0, errors.New("response ID printed before state was saved")
	}
	return w.output.Write(p)
}

func TestPersistAndPrintBackgroundProgressSavesBeforePrinting(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{}
	writer := &afterSaveWriter{store: store}
	printedID := ""
	err := persistAndPrintBackgroundProgress(
		t.Context(),
		store,
		"agent-key",
		savedBackgroundResponse{ResponseID: "resp_123", Status: "in_progress"},
		&printedID,
		writer,
	)

	require.NoError(t, err)
	assert.True(t, store.saved)
	assert.Equal(t, "resp_123", printedID)
	assert.Equal(t, "Response:     resp_123\n", writer.output.String())
}

func TestPersistAndPrintBackgroundProgressDoesNotPrintWhenSaveFails(t *testing.T) {
	t.Parallel()

	store := &orderingResponseStore{saveErr: errors.New("write failed")}
	writer := &afterSaveWriter{store: store}
	printedID := ""
	err := persistAndPrintBackgroundProgress(
		t.Context(),
		store,
		"agent-key",
		savedBackgroundResponse{ResponseID: "resp_123"},
		&printedID,
		writer,
	)

	require.EqualError(t, err, "write failed")
	assert.Empty(t, writer.output.String())
	assert.Empty(t, printedID)
}

func TestInvokeCommandBackgroundValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rejects local",
			args: []string{"--background", "--local", "hello"},
			want: "supported only for remote Responses agents",
		},
		{
			name: "rejects explicit invocations protocol",
			args: []string{"--background", "--protocol", "invocations", "hello"},
			want: "--background is not supported with the invocations protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tt.want), "error %q should contain %q", err, tt.want)
		})
	}
}
