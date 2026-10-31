// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"azure.springboot/internal/spring"
)

func TestConfirmPreservesPromptFailures(t *testing.T) {
	for _, failure := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		status.Error(codes.Canceled, "prompt cancelled"),
		errors.New("prompt transport failed"),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			err := confirm(t.Context(), func() (initPrompter, error) {
				return &fakePrompter{err: failure}, nil
			}, "Continue?")
			require.ErrorIs(t, err, failure)
			_, classified := errors.AsType[*azdext.LocalError](err)
			require.False(t, classified)
		})
	}
}

type fakeHostPrompt struct {
	azdext.PromptServiceClient
	selectRequest *azdext.SelectRequest
	promptRequest *azdext.PromptRequest
	selection     *int32
	text          string
}

func (p *fakeHostPrompt) Select(
	_ context.Context, request *azdext.SelectRequest, _ ...grpc.CallOption,
) (*azdext.SelectResponse, error) {
	p.selectRequest = request
	return &azdext.SelectResponse{Value: p.selection}, nil
}

func (p *fakeHostPrompt) Prompt(
	_ context.Context, request *azdext.PromptRequest, _ ...grpc.CallOption,
) (*azdext.PromptResponse, error) {
	p.promptRequest = request
	return &azdext.PromptResponse{Value: p.text}, nil
}

func TestHostProfileSelection(t *testing.T) {
	for _, tt := range []struct {
		name      string
		selection *int32
		text      string
		expected  string
		errorText string
	}{
		{"evidenced profile", new(int32(1)), "", "postgres", ""},
		{"manual profiles", new(int32(2)), "shared,postgres", "shared,postgres", ""},
		{"absent answer", nil, "", "", "no valid answer"},
		{"negative index", new(int32(-1)), "", "", "no valid answer"},
		{"outside choices", new(int32(3)), "", "", "no valid answer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHostPrompt{selection: tt.selection, text: tt.text}
			prompts := &hostPrompter{service: service}
			answer, err := prompts.Decide(t.Context(), spring.Decision{
				ID: "profiles", Profiles: true, Choices: []string{"mysql", "postgres"},
			})
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, answer)
			}
			require.Len(t, service.selectRequest.Options.Choices, 3)
			if tt.name == "manual profiles" {
				require.NotNil(t, service.promptRequest)
			}
		})
	}
}

func TestHostDatasourceInputIsHidden(t *testing.T) {
	service := &fakeHostPrompt{text: "jdbc:h2:mem:catalog"}
	prompts := &hostPrompter{service: service}
	answer, err := prompts.Decide(t.Context(), spring.Decision{
		Input: "DEPLOYMENT_JDBC_URL",
		Evidence: []spring.Evidence{{
			Property: "spring.datasource.url", Source: "application.properties",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, service.text, answer)
	require.True(t, service.promptRequest.Options.Secret)
	require.Contains(t, service.promptRequest.Options.HelpMessage, "spring.datasource.url")
	require.NotContains(t, service.promptRequest.Options.Message, service.text)
}
