// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubSkillDeleteClient struct {
	err   error
	names []string
}

func (s *stubSkillDeleteClient) DeleteSkill(_ context.Context, name string) (*skill_api.DeleteSkillResponse, error) {
	s.names = append(s.names, name)
	return &skill_api.DeleteSkillResponse{Name: name, Deleted: s.err == nil}, s.err
}

func TestDeleteSkillAndClearMarkers(t *testing.T) {
	t.Run("clears after successful deletion", func(t *testing.T) {
		client := &stubSkillDeleteClient{}
		var cleared []string
		previous := clearSkillMarkersFunc
		clearSkillMarkersFunc = func(_ context.Context, name, endpoint string) error {
			cleared = append(cleared, name)
			require.Equal(t, "https://example.test/projects/current", endpoint)
			return nil
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.NoError(t, deleteSkillAndClearMarkers(
			t.Context(), client, "my-skill", "https://example.test/projects/current",
		))
		require.Equal(t, []string{"my-skill"}, client.names)
		require.Equal(t, []string{"my-skill"}, cleared)
	})

	t.Run("preserves markers after failed deletion", func(t *testing.T) {
		client := &stubSkillDeleteClient{err: errors.New("delete failed")}
		called := false
		previous := clearSkillMarkersFunc
		clearSkillMarkersFunc = func(context.Context, string, string) error {
			called = true
			return nil
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.Error(t, deleteSkillAndClearMarkers(
			t.Context(), client, "my-skill", "https://example.test/projects/current",
		))
		require.False(t, called)
	})

	t.Run("not found retries marker cleanup", func(t *testing.T) {
		client := &stubSkillDeleteClient{err: &azcore.ResponseError{StatusCode: http.StatusNotFound}}
		called := false
		previous := clearSkillMarkersFunc
		clearSkillMarkersFunc = func(context.Context, string, string) error {
			called = true
			return nil
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.NoError(t, deleteSkillAndClearMarkers(
			t.Context(), client, "my-skill", "https://example.test/projects/current",
		))
		require.True(t, called)
	})

	t.Run("cleanup failure does not hide successful deletion", func(t *testing.T) {
		client := &stubSkillDeleteClient{}
		previous := clearSkillMarkersFunc
		clearSkillMarkersFunc = func(context.Context, string, string) error {
			return errors.New("cleanup failed")
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.NoError(t, deleteSkillAndClearMarkers(
			t.Context(), client, "my-skill", "https://example.test/projects/current",
		))
	})
}

func TestSameSkillProjectEndpoint(t *testing.T) {
	t.Parallel()
	require.True(t, sameSkillProjectEndpoint(
		"HTTPS://ACCOUNT.TEST/projects/current/",
		"https://account.test/projects/current",
	))
	require.True(t, sameSkillProjectEndpoint(
		"https://account.test/api/projects/current",
		"https://account.test/projects/current",
	))
	require.False(t, sameSkillProjectEndpoint(
		"https://account.test/projects/old",
		"https://account.test/projects/current",
	))
}

func TestIsNoSkillAzdEnvironment(t *testing.T) {
	t.Parallel()
	require.True(t, isNoSkillAzdEnvironment(status.Error(codes.NotFound, "environment not found")))
	require.True(t, isNoSkillAzdEnvironment(status.Error(codes.Unknown, "default environment not found")))
	require.False(t, isNoSkillAzdEnvironment(status.Error(codes.Unavailable, "daemon unavailable")))
}

func TestClearSkillMarkerValues(t *testing.T) {
	values := map[string]string{
		"SKILL_MY_SKILL_VERSION":          "3",
		"SKILL_MY_SKILL_PROJECT_ENDPOINT": "https://example.test/projects/current",
	}
	err := clearSkillMarkerValues("my-skill", func(key, value string) error {
		values[key] = value
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, values["SKILL_MY_SKILL_VERSION"])
	require.Empty(t, values["SKILL_MY_SKILL_PROJECT_ENDPOINT"])
}

func TestDeleteAction_RejectsInvalidName(t *testing.T) {
	a := &deleteAction{flags: &deleteFlags{name: "_bad"}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeInvalidSkillName, le.Code)
}

func TestDeleteAction_NoPromptRequiresForce(t *testing.T) {
	a := &deleteAction{flags: &deleteFlags{name: "my-skill", noPrompt: true, force: false}}
	err := a.Run(context.Background())
	require.Error(t, err)
	var le *azdext.LocalError
	require.True(t, errors.As(err, &le))
	require.Equal(t, exterrors.CodeMissingForceFlag, le.Code)
}
