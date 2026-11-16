// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ferrors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrors(t *testing.T) {
	var nilErr *Error
	assert.Empty(t, nilErr.Error())
	assert.NoError(t, nilErr.Unwrap())

	v := InvalidFileRefValidation("bad ref", "fix it")
	assert.Equal(t, CodeInvalidFileRef, v.Code)
	assert.Equal(t, CategoryValidation, v.Category)
	assert.Equal(t, "bad ref", v.Error())
	assert.Equal(t, "fix it", v.Suggestion)

	internalErr := InvalidFileRefInternal("unexpected")
	assert.Equal(t, CodeInvalidFileRef, internalErr.Code)
	assert.Equal(t, CategoryInternal, internalErr.Category)
	assert.Equal(t, "unexpected", internalErr.Error())

	authErr := Auth(CodeAuthFailed, "could not authenticate", "run azd auth login")
	assert.Equal(t, CategoryAuth, authErr.Category)
	assert.Equal(t, CodeAuthFailed, authErr.Code)

	cause := errors.New("underlying secret=123")
	svcErr := Service("get_toolbox.PermissionDenied", 403, "foundry", "request failed", "assign role", cause)
	assert.Equal(t, CategoryService, svcErr.Category)
	assert.Equal(t, 403, svcErr.StatusCode)
	assert.Equal(t, "foundry", svcErr.ServiceName)
	assert.NotContains(t, svcErr.Error(), "secret=123")
	require.ErrorIs(t, svcErr, cause)

	policyCodes := []struct {
		code string
		want string
	}{
		{CodeAgentPolicyBlocked, "agent_policy_blocked"},
		{CodeAgentKindBlockedByPolicy, "agent_kind_blocked_by_policy"},
		{CodeContainerImageBlockedByPolicy, "container_image_blocked_by_policy"},
		{CodeModelBlockedByPolicy, "model_blocked_by_policy"},
		{CodeToolBlockedByPolicy, "tool_blocked_by_policy"},
		{CodeToolboxBlockedByPolicy, "toolbox_blocked_by_policy"},
	}
	for _, tc := range policyCodes {
		err := Validation(tc.code, "blocked by policy", "update configuration")
		assert.Equal(t, tc.want, err.Code)
		assert.Equal(t, CategoryValidation, err.Category)
		assert.Equal(t, "blocked by policy", err.Error())
		assert.Equal(t, "update configuration", err.Suggestion)
	}
}
