// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type fakeAgentGetter struct {
	err error
}

func (f fakeAgentGetter) GetAgent(context.Context, string, string) (*agent_api.AgentObject, error) {
	if f.err != nil {
		return nil, f.err
	}

	return &agent_api.AgentObject{}, nil
}

func TestAgentExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantExists bool
		wantErr    bool
	}{
		{name: "exists", wantExists: true},
		{
			name: "not found",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "not_found",
			},
		},
		{
			name: "service error",
			err: &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
				ErrorCode:  "internal_error",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := AgentExists(t.Context(), fakeAgentGetter{err: tt.err}, "my-agent", "v1")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exists != tt.wantExists {
				t.Fatalf("exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestExistingAgentWarning(t *testing.T) {
	t.Parallel()

	warning := ExistingAgentWarning("my-agent")
	for _, want := range []string{
		"my-agent",
		"create a new version of the existing agent",
		"not a separate agent",
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning %q missing %q", warning, want)
		}
	}
}
