// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// azd answers "no default environment" and "no such key" with plain Go errors.
// Its interceptor only rewrites errors carrying a suggestion or an auth
// failure, so everything else reaches the client as Unknown. Reading Unknown as
// a failure would stop a project with no environment selected from ever
// reaching the global config or the host variable.
func TestUnansweredHostedSourcesLetTheCascadeCarryOn(t *testing.T) {
	for name, err := range map[string]error{
		"no daemon at all":       status.Error(codes.Unavailable, "connection refused"),
		"nothing under that key": status.Error(codes.NotFound, "key not found"),
		"no default environment": status.Error(codes.Unknown, "default environment not found"),
		"wrapped in context": fmt.Errorf("reading the environment: %w",
			status.Error(codes.Unknown, "default environment not found")),
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, hostedSourceAbsent(err),
				"this is absence, so levels 3 and 4 still have to be consulted")
		})
	}
}

// A daemon that refused, or one that broke, is not a daemon with nothing to
// say. Falling through here would resolve to a lower-priority endpoint that can
// belong to a different project, and nothing would have said so.
func TestAFailureToAnswerIsReportedRatherThanSkipped(t *testing.T) {
	for name, err := range map[string]error{
		"the login has expired":  status.Error(codes.Unauthenticated, "the login has expired"),
		"not allowed":            status.Error(codes.PermissionDenied, "forbidden"),
		"the user hit ctrl-c":    status.Error(codes.Canceled, "context canceled"),
		"the read timed out":     status.Error(codes.DeadlineExceeded, "deadline exceeded"),
		"the daemon broke":       status.Error(codes.Internal, "internal error"),
		"the daemon is full":     status.Error(codes.ResourceExhausted, "quota exceeded"),
		"the answer was corrupt": status.Error(codes.DataLoss, "data loss"),
		// A bare error carries no status at all, so it never travelled the wire
		// as an absence the daemon reported.
		"not a status at all": errors.New("something local went wrong"),
		"wrapped expiry": fmt.Errorf("reading the environment: %w",
			status.Error(codes.Unauthenticated, "expired")),
		"nested twice over": fmt.Errorf("outer: %w",
			fmt.Errorf("inner: %w", status.Error(codes.PermissionDenied, "no"))),
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, hostedSourceAbsent(err),
				"a failure to answer has to surface, not resolve to a different project")
		})
	}
}
