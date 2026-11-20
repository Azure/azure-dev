// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportErrPreservesCauses(t *testing.T) {
	for _, cause := range []error{
		errors.New("authentication challenge failed"),
		errors.New("cannot decode resource response"),
		fmt.Errorf("request canceled: %w", context.Canceled),
		fmt.Errorf("request timed out: %w", context.DeadlineExceeded),
		errors.New("connection reset at https://cause-user:cause-password@example.test/resource" +
			"?sig=cause-secret#cause-fragment"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			report := Report{{
				ID: "resource-refs", Status: Unverified, Mandatory: true,
				Observed: "resource could not be read", Cause: cause,
			}}
			err := report.Err()
			require.ErrorIs(t, err, cause)
			blocked, ok := errors.AsType[*BlockedError](err)
			require.True(t, ok)
			require.Len(t, blocked.Checks, 1)
			assert.Same(t, cause, blocked.Checks[0].Cause)
			for _, secret := range []string{"cause-user", "cause-password", "sig=", "cause-secret", "cause-fragment"} {
				assert.NotContains(t, err.Error(), secret)
			}
			assert.Same(t, cause, report[0].Cause)
			assert.Equal(t, Unverified, report[0].Status)
			for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
				assert.Equal(t, errors.Is(cause, cancellation), errors.Is(err, cancellation))
			}
		})
	}
}
