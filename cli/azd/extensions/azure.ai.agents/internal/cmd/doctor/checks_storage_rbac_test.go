// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestClassifyProjectStorageRBAC(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		statuses []string
		want     Status
	}{
		{"managed storage", nil, StatusSkip},
		{"granted", []string{"granted"}, StatusPass},
		{"missing", []string{"missing"}, StatusFail},
		{"invalid", []string{"invalid"}, StatusFail},
		{"unknown", []string{"unknown"}, StatusWarn},
		{"key", []string{"skip"}, StatusSkip},
		{"mixed success", []string{"granted", "skip"}, StatusPass},
		{"mixed unknown", []string{"granted", "unknown"}, StatusWarn},
		{"mixed failure", []string{"granted", "unknown", "missing"}, StatusFail},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := &project.ProjectStorageRBACResult{PrincipalID: "private-principal"}
			for _, status := range testCase.statuses {
				input.Findings = append(input.Findings, project.StorageRBACFinding{
					ConnectionName: "private-connection", StorageScope: "private-scope", Status: status, Message: "finding",
				})
			}
			for _, unredacted := range []bool{false, true} {
				result := classifyProjectStorageRBAC(input, unredacted)
				require.Equal(t, testCase.want, result.Status)
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				if !unredacted {
					require.NotContains(t, string(encoded), "private-")
				} else if len(testCase.statuses) > 0 {
					require.Contains(t, string(encoded), "private-principal")
					require.Contains(t, string(encoded), "private-scope")
				}
			}
		})
	}
	require.Equal(t, StatusWarn, classifyProjectStorageRBAC(nil, false).Status)
}

func TestProjectStorageRBACQueryError(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		err  error
		want Status
	}{
		{context.Canceled, StatusSkip},
		{context.DeadlineExceeded, StatusWarn},
		{project.ErrInvalidProjectResourceID, StatusFail},
		{errors.New("403 https://user:secret@example.invalid?sig=secret"), StatusWarn},
	} {
		result := storageRBACQueryError(testCase.err)
		require.Equal(t, testCase.want, result.Status)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "secret")
	}
}

func TestCheckProjectStorageRBAC(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		noClient   bool
		localOnly  bool
		cancelled  bool
		projectID  string
		readErr    error
		probeErr   error
		prior      []Result
		want       Status
		probeCalls int
	}{
		{name: "normal", projectID: "project", want: StatusPass, probeCalls: 1},
		{name: "no client", noClient: true, want: StatusSkip},
		{name: "local only", localOnly: true, want: StatusSkip},
		{name: "cancelled", cancelled: true, want: StatusSkip},
		{name: "no project", want: StatusSkip},
		{name: "read failure", readErr: errors.New("secret"), want: StatusWarn},
		{name: "probe failure", projectID: "project", probeErr: errors.New("403 secret"), want: StatusWarn, probeCalls: 1},
		{name: "auth failed", projectID: "project",
			prior: []Result{{ID: "remote.auth", Status: StatusFail}}, want: StatusSkip},
		{name: "environment failed", projectID: "project",
			prior: []Result{{ID: "local.environment-selected", Status: StatusFail}}, want: StatusSkip},
		{name: "independent of developer and endpoint", projectID: "project", want: StatusPass, probeCalls: 1,
			prior: []Result{{ID: "remote.rbac", Status: StatusSkip}, {ID: "remote.foundry-endpoint", Status: StatusFail}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			deps := Dependencies{
				AzdClient: &azdext.AzdClient{},
				readProjectResourceIDFn: func(context.Context, *azdext.AzdClient) (string, error) {
					return testCase.projectID, testCase.readErr
				},
				probeProjectStorageRBAC: func(
					context.Context, *azdext.AzdClient, string,
				) (*project.ProjectStorageRBACResult, error) {
					calls++
					return &project.ProjectStorageRBACResult{
						Findings: []project.StorageRBACFinding{{Status: "granted"}},
					}, testCase.probeErr
				},
			}
			if testCase.noClient {
				deps.AzdClient = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.cancelled {
				cancel()
			}
			check := newCheckProjectStorageRBAC(deps)
			result := check.Fn(ctx, Options{LocalOnly: testCase.localOnly}, testCase.prior)
			require.Equal(t, testCase.want, result.Status)
			require.Equal(t, testCase.probeCalls, calls)
			require.NotContains(t, result.Message, "secret")
		})
	}
}

func TestProjectStorageRBACRemediation(t *testing.T) {
	t.Parallel()
	result := classifyProjectStorageRBAC(&project.ProjectStorageRBACResult{
		PrincipalID: "project-principal",
		Findings: []project.StorageRBACFinding{{
			ConnectionName: "storage", StorageScope: "storage-scope", Status: "missing",
			Message: "required Blob data role assignment is missing",
		}},
	}, true)
	require.Contains(t, result.Message, "project-principal")
	require.Contains(t, result.Message, "storage-scope")
	require.Contains(t, result.Suggestion, "permission to assign roles")
	require.Contains(t, result.Suggestion, "Storage Blob Data Contributor")
	require.Contains(t, result.Suggestion, "project-principal")
	require.Contains(t, result.Suggestion, "storage-scope")
}

func TestProjectStorageRBACIncompleteConnectionName(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"invalid", "unknown"} {
		for _, unredacted := range []bool{false, true} {
			result := classifyProjectStorageRBAC(&project.ProjectStorageRBACResult{
				PrincipalID: "private-principal",
				Findings: []project.StorageRBACFinding{{
					ConnectionName: "private-connection", Status: status, Message: "storage metadata is incomplete",
				}},
			}, unredacted)
			require.Contains(t, result.Message, "Connection")
			if unredacted {
				require.Contains(t, result.Message, "private-connection")
			} else {
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "private-")
			}
		}
	}
}

func TestProjectStorageRBACMixedFailureRemediation(t *testing.T) {
	t.Parallel()
	result := classifyProjectStorageRBAC(&project.ProjectStorageRBACResult{
		Findings: []project.StorageRBACFinding{
			{ConnectionName: "missing-role", Status: "missing"},
			{ConnectionName: "invalid-connection", Status: "invalid"},
		},
	}, false)
	require.Equal(t, StatusFail, result.Status)
	suggestion := firstLine(result.Suggestion)
	require.Contains(t, suggestion, "grant Storage Blob Data Contributor")
	require.Contains(t, suggestion, "correct the invalid project identity or Storage connection")
}
