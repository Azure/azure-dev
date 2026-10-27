// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPortableErrors(t *testing.T) {
	for _, code := range []Code{InvalidOptions, InvalidTarget, IdentityUnavailable, TargetMismatch,
		ResolutionFailed, ProbeFailed, GrantFailed, PropagationFailed} {
		t.Run(string(code), func(t *testing.T) {
			cause := errors.Join(context.DeadlineExceeded, errors.New("https://user:secret@example.com?sig=secret"))
			err := failure(code, "safe message", cause)
			shared, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			require.Equal(t, code, shared.Code)
			require.NotEmpty(t, shared.Kind)
			require.NotEmpty(t, shared.Suggestion)
			require.NotContains(t, err.Error(), "secret")
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Same(t, shared, failure(ResolutionFailed, "outer context", err))
		})
	}
}
