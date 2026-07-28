// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
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
		clearSkillMarkersFunc = func(_ context.Context, name string) error {
			cleared = append(cleared, name)
			return nil
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.NoError(t, deleteSkillAndClearMarkers(t.Context(), client, "my-skill"))
		require.Equal(t, []string{"my-skill"}, client.names)
		require.Equal(t, []string{"my-skill"}, cleared)
	})

	t.Run("preserves markers after failed deletion", func(t *testing.T) {
		client := &stubSkillDeleteClient{err: errors.New("delete failed")}
		called := false
		previous := clearSkillMarkersFunc
		clearSkillMarkersFunc = func(context.Context, string) error {
			called = true
			return nil
		}
		t.Cleanup(func() { clearSkillMarkersFunc = previous })

		require.Error(t, deleteSkillAndClearMarkers(t.Context(), client, "my-skill"))
		require.False(t, called)
	})
}

func TestClearSkillMarkersSeam(t *testing.T) {
	called := false
	previous := clearSkillMarkersFunc
	clearSkillMarkersFunc = func(context.Context, string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { clearSkillMarkersFunc = previous })

	require.NoError(t, clearSkillMarkersFunc(t.Context(), "my-skill"))
	require.True(t, called)
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
