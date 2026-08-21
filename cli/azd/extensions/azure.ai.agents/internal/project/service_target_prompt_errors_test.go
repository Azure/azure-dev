// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestIsWorkspaceNotFoundError covers the classifier that decides whether the
// deploy path should fall back to creating the AML workspace. A false negative
// aborts the deploy; a false positive triggers a needless create.
func TestIsWorkspaceNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "response error code",
			err:  &azcore.ResponseError{ErrorCode: "WorkspaceNotFound", StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "response error code is case-insensitive",
			err:  &azcore.ResponseError{ErrorCode: "workspacenotfound"},
			want: true,
		},
		{
			name: "wrapped response error",
			err: fmt.Errorf("resolving workspace: %w",
				&azcore.ResponseError{ErrorCode: "WorkspaceNotFound"}),
			want: true,
		},
		{name: "message fallback", err: errors.New("the workspace not found in group rg"), want: true},
		{name: "unrelated 404", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, want: false},
		{name: "unrelated error", err: errors.New("forbidden"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWorkspaceNotFoundError(tt.err); got != tt.want {
				t.Errorf("isWorkspaceNotFoundError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsAgentConflictError covers the classifier that switches the publish path
// from create to new-version. Misclassifying here either fails a re-deploy or
// silently skips the create.
func TestIsAgentConflictError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "409 status", err: &azcore.ResponseError{StatusCode: http.StatusConflict}, want: true},
		{name: "conflict error code", err: &azcore.ResponseError{ErrorCode: "Conflict"}, want: true},
		{
			name: "wrapped 409",
			err:  fmt.Errorf("creating agent: %w", &azcore.ResponseError{StatusCode: http.StatusConflict}),
			want: true,
		},
		{name: "message fallback", err: errors.New(`agent "x" already exists`), want: true},
		{name: "404 is not a conflict", err: &azcore.ResponseError{StatusCode: http.StatusNotFound}, want: false},
		{name: "unrelated error", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAgentConflictError(tt.err); got != tt.want {
				t.Errorf("isAgentConflictError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
