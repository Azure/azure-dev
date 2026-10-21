// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCUClient struct {
	analyzer cuclient.Analyzer
	err      error
	ids      []string
}

var _ cuclient.Client = (*fakeCUClient)(nil)

var errUnexpectedClientCall = errors.New("unexpected client operation in contract test")

func (f *fakeCUClient) GetAnalyzer(ctx context.Context, id string) (cuclient.Analyzer, error) {
	if err := ctx.Err(); err != nil {
		return cuclient.Analyzer{}, &cuclient.Error{Kind: cuclient.ErrorCancelled, Message: "Cancelled.", Cause: err}
	}
	f.ids = append(f.ids, id)
	return f.analyzer, f.err
}

func (*fakeCUClient) ListAnalyzers(context.Context) ([]cuclient.Analyzer, error) {
	return nil, errUnexpectedClientCall
}

func (*fakeCUClient) CreateAnalyzer(context.Context, cuclient.CreateAnalyzerRequest) (cuclient.AnalyzerResponse, error) {
	return cuclient.AnalyzerResponse{}, errUnexpectedClientCall
}

func (*fakeCUClient) DeleteAnalyzer(context.Context, string) error {
	return errUnexpectedClientCall
}

func (*fakeCUClient) GrantCopyAuthorization(
	context.Context, cuclient.GrantCopyAuthorizationRequest,
) (cuclient.CopyAuthorization, error) {
	return cuclient.CopyAuthorization{}, errUnexpectedClientCall
}

func (*fakeCUClient) CopyAnalyzer(context.Context, cuclient.CopyAnalyzerRequest) (cuclient.AnalyzerResponse, error) {
	return cuclient.AnalyzerResponse{}, errUnexpectedClientCall
}

func (*fakeCUClient) GetDefaults(context.Context) (cuclient.Defaults, error) {
	return cuclient.Defaults{}, errUnexpectedClientCall
}

func (*fakeCUClient) UpdateDefaults(context.Context, cuclient.UpdateDefaultsRequest) (cuclient.Defaults, error) {
	return cuclient.Defaults{}, errUnexpectedClientCall
}

func (*fakeCUClient) Analyze(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
	return cuclient.AnalyzeResponse{}, errUnexpectedClientCall
}

func contractTestCommand(client cuclient.Client) *cobra.Command {
	return &cobra.Command{
		Use: "contract-test <id>", Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cuclient.ValidateAnalyzerID(args[0]); err != nil {
				return exterrors.FromError(err)
			}
			analyzer, err := client.GetAnalyzer(cmd.Context(), args[0])
			if err != nil {
				return exterrors.FromError(err)
			}
			_, err = cmd.OutOrStdout().Write(analyzer.JSON)
			return err
		},
	}
}

func TestFakeClientCommandInjection(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	payload := []byte(`{"analyzerId":"invoice","fieldSchema":{"fields":{"custom":{"type":"json"}}}}`)
	client := &fakeCUClient{analyzer: cuclient.Analyzer{ID: "invoice", JSON: payload}}
	root := NewRootCommand()
	root.AddCommand(contractTestCommand(client))
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"contract-test", "invoice"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	assert.Equal(t, []string{"invoice"}, client.ids)
	assert.Equal(t, payload, stdout.Bytes())
	assert.Empty(t, stderr.String())
}

func TestFakeClientCommandErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		id     string
		err    error
		cancel bool
	}{
		{"not found", "invoice", &cuclient.Error{
			Kind: cuclient.ErrorNotFound, Code: "NotFound", StatusCode: 404,
			Message: "Analyzer not found.", Suggestion: "Check the analyzer ID.",
		}, false},
		{"invalid request", "../invoice", nil, false},
		{"cancelled", "invoice", nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeCUClient{err: tt.err}
			root := NewRootCommand()
			root.AddCommand(contractTestCommand(client))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs([]string{"contract-test", tt.id})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			err := root.ExecuteContext(ctx)
			require.Error(t, err)
			assert.Empty(t, stdout.String())
			assert.Empty(t, stderr.String())
			if tt.cancel {
				assert.ErrorIs(t, err, context.Canceled)
				assert.Empty(t, client.ids)
			} else if tt.err != nil {
				service, ok := errors.AsType[*azdext.ServiceError](err)
				require.True(t, ok)
				assert.Equal(t, 404, service.StatusCode)
				assert.Equal(t, "Check the analyzer ID.", service.Suggestion)
			} else {
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
				assert.Empty(t, client.ids)
			}
		})
	}
}
