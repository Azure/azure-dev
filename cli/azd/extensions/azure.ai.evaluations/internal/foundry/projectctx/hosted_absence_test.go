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
		"no daemon at all":         status.Error(codes.Unavailable, "connection refused"),
		"nothing under that key":   status.Error(codes.NotFound, "key not found"),
		"no default environment":   status.Error(codes.Unknown, "default environment not found"),
		"a plain error, unwrapped": errors.New("default environment not found"),
		"wrapped in context": fmt.Errorf("reading the environment: %w",
			status.Error(codes.Unknown, "default environment not found")),
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, hostedSourceAbsent(err),
				"this is absence, so levels 3 and 4 still have to be consulted")
		})
	}
}

// A daemon that refused is not a daemon with nothing to say. Falling through
// here would resolve to a lower-priority endpoint belonging to another project,
// and nothing would have said so.
func TestARefusedReadIsReportedRatherThanSkipped(t *testing.T) {
	for name, err := range map[string]error{
		"expired login":       status.Error(codes.Unauthenticated, "reauthentication required"),
		"not allowed":         status.Error(codes.PermissionDenied, "forbidden"),
		"the user hit ctrl-c": status.Error(codes.Canceled, "context canceled"),
		"the read timed out":  status.Error(codes.DeadlineExceeded, "deadline exceeded"),
		"wrapped expiry":      fmt.Errorf("reading the environment: %w", status.Error(codes.Unauthenticated, "expired")),
		"nested twice over":   fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", status.Error(codes.PermissionDenied, "no"))),
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, hostedSourceAbsent(err),
				"a refusal has to surface, not resolve to a different project")
		})
	}
}
