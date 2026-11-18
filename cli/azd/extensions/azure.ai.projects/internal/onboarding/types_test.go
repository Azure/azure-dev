// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportWriteIncludesRedactedCause(t *testing.T) {
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
			var output strings.Builder
			require.NoError(t, report.Write(&output))
			assert.Contains(t, output.String(), "  Cause: "+RedactDiagnostic(cause.Error())+"\n")
			for _, secret := range []string{"cause-user", "cause-password", "sig=", "cause-secret", "cause-fragment"} {
				assert.NotContains(t, output.String(), secret)
			}
			require.ErrorIs(t, report.Err(), cause)
			assert.Same(t, cause, report[0].Cause)
			assert.Equal(t, Unverified, report[0].Status)
			for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
				assert.Equal(t, errors.Is(cause, cancellation), errors.Is(report.Err(), cancellation))
			}
		})
	}
}

func TestReportWriteWithoutCauseRetainsOutput(t *testing.T) {
	report := Report{{ID: "resource-refs", Scope: "scope", Status: Pass, Expected: "readable", Observed: "read"}}
	var output strings.Builder
	require.NoError(t, report.Write(&output))
	assert.Equal(t, "resource-refs PASS [scope]\n  Expected: readable\n  Observed: read\n"+
		"  Missing evidence/permission: \n  Remediation: \n", output.String())
}

type causeErrorWriter struct{}

func (causeErrorWriter) Write(value []byte) (int, error) {
	if strings.HasPrefix(string(value), "  Cause:") {
		return 0, io.ErrClosedPipe
	}
	return len(value), nil
}

func TestReportWritePropagatesCauseWriterFailure(t *testing.T) {
	report := Report{{ID: "resource-refs", Status: Unverified, Cause: errors.New("read failed")}}
	require.ErrorIs(t, report.Write(causeErrorWriter{}), io.ErrClosedPipe)
}
