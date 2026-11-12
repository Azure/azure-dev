// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type evalPickerServer struct {
	azdext.UnimplementedPromptServiceServer
	response *azdext.SelectResponse
	err      error
	requests chan *azdext.SelectRequest
}

func (s *evalPickerServer) Select(
	_ context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.requests <- req
	return s.response, s.err
}

func serveEvalPicker(t *testing.T, response *azdext.SelectResponse, promptErr error) *evalPickerServer {
	t.Helper()
	picker := &evalPickerServer{
		response: response,
		err:      promptErr,
		requests: make(chan *azdext.SelectRequest, 1),
	}
	server := grpc.NewServer()
	azdext.RegisterPromptServiceServer(server, picker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_NO_PROMPT", "false")
	return picker
}

func writePickerConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`datasets:
  - name: rows
evals:
  - name: local-alpha
    dataset: rows
    evaluators:
      - evaluator: builtin.groundedness
  - name: local-beta
    dataset: rows
    evaluators:
      - evaluator: builtin.relevance
`), 0o600))
	cfg, err := project.LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	return dir
}

func pickerCommand(t *testing.T, command, dir string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var cmd *cobra.Command
	switch command {
	case "create":
		cmd = newEvalCreateCommand()
	case "run start":
		cmd = buildRunCommand("start", "Start a run.")
	case "run list":
		cmd = newRunListCommand()
	default:
		t.Fatalf("unknown test command %q", command)
	}
	cmd.SetContext(t.Context())
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetArgs([]string{"--path", dir, "--project-endpoint", "https://test.services.ai.azure.com/api/projects/test"})
	return cmd, out
}

func TestEvalPickerCallersDistinguishCancelFromPromptErrors(t *testing.T) {
	tests := []struct {
		name     string
		response *azdext.SelectResponse
		err      error
		wantCode codes.Code
	}{
		{
			name:     "explicit Cancel",
			response: &azdext.SelectResponse{Value: new(int32(2))},
			wantCode: codes.OK,
		},
		{
			// This is the host UX error for Ctrl+C, serialized by a real gRPC
			// server while the extension's command context remains live.
			name:     "host interrupt",
			err:      errors.Join(ux.ErrCancelled, context.Canceled),
			wantCode: codes.Canceled,
		},
		{
			name:     "transport failure",
			err:      status.Error(codes.Unavailable, "prompt unavailable"),
			wantCode: codes.Unavailable,
		},
		{
			name:     "prompt failure",
			err:      errors.New("prompt failed"),
			wantCode: codes.Unknown,
		},
	}
	for _, command := range []string{"create", "run start", "run list"} {
		for _, tt := range tests {
			t.Run(command+"/"+tt.name, func(t *testing.T) {
				picker := serveEvalPicker(t, tt.response, tt.err)
				dir := writePickerConfig(t)
				path := filepath.Join(dir, "azure.eval.yaml")
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				cmd, out := pickerCommand(t, command, dir)

				err = cmd.Execute()

				require.NoError(t, cmd.Context().Err(), "the host interrupted only its prompt")
				assert.Equal(t, tt.wantCode, status.Code(err))
				if tt.wantCode == codes.OK {
					require.NoError(t, err)
					assert.Equal(t, messages.EvalSelectionCancelled(), out.String())
				} else {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.err.Error())
					assert.False(t, isEvalSelectionCancelled(err))
					assert.Empty(t, out.String())
				}
				require.Len(t, picker.requests, 1)
				req := <-picker.requests
				require.Len(t, req.Options.Choices, 3)
				assert.Equal(t, "local-alpha", req.Options.Choices[0].Value)
				assert.Equal(t, "local-beta", req.Options.Choices[1].Value)
				assert.Equal(t, "Cancel", req.Options.Choices[2].Label)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}

func TestEvalPickerCallersKeepNonInteractiveAmbiguity(t *testing.T) {
	for _, command := range []string{"create", "run start", "run list"} {
		for _, flag := range []string{"no-prompt", "output"} {
			t.Run(command+"/"+flag, func(t *testing.T) {
				picker := serveEvalPicker(t, nil, errors.New("must not prompt"))
				cmd, out := pickerCommand(t, command, writePickerConfig(t))
				value := "true"
				if flag == "output" {
					value = "json"
				}
				require.NoError(t, cmd.Flags().Set(flag, value))

				err := cmd.Execute()

				require.Error(t, err)
				assert.Contains(t, err.Error(), "local-alpha")
				assert.Contains(t, err.Error(), "local-beta")
				assert.Contains(t, err.Error(), "--eval")
				assert.Empty(t, out.String())
				assert.Empty(t, picker.requests)
			})
		}
	}
}

func TestEvalPickerCallersPropagateCommandCancellation(t *testing.T) {
	for _, command := range []string{"create", "run start", "run list"} {
		t.Run(command, func(t *testing.T) {
			picker := serveEvalPicker(t, nil, errors.New("must not prompt"))
			cmd, out := pickerCommand(t, command, writePickerConfig(t))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			cmd.SetContext(ctx)

			err := cmd.Execute()

			require.ErrorIs(t, err, context.Canceled)
			assert.False(t, isEvalSelectionCancelled(err))
			assert.Empty(t, out.String())
			assert.Empty(t, picker.requests)
		})
	}
}

func TestChooseEvalRPCPreservesSelectionAndUnansweredResponses(t *testing.T) {
	tests := []struct {
		name     string
		response *azdext.SelectResponse
		want     string
	}{
		{"first eval", &azdext.SelectResponse{Value: new(int32(0))}, "cancel"},
		{"second eval", &azdext.SelectResponse{Value: new(int32(1))}, "other"},
		{"unset value", &azdext.SelectResponse{}, ""},
		{"negative index", &azdext.SelectResponse{Value: new(int32(-1))}, ""},
		{"out of range", &azdext.SelectResponse{Value: new(int32(3))}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serveEvalPicker(t, tt.response, nil)
			cmd := newEvalCreateCommand()
			cmd.SetContext(t.Context())

			got, err := chooseEval(cmd, configWith("cancel", "other"), "")

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
