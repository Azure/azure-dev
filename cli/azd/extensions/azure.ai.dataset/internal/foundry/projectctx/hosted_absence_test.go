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

// azd answers "no default environment" and "no such environment" with plain Go
// errors. Its interceptor only rewrites errors carrying a suggestion or an auth
// failure, so everything else reaches the client as Unknown. Reading Unknown as
// a failure would stop a project with no environment selected from ever
// reaching the global config or the host variable.
func TestUnansweredHostedSourcesLetTheCascadeCarryOn(t *testing.T) {
	for name, err := range map[string]error{
		"no daemon at all":       status.Error(codes.Unavailable, "connection refused"),
		"nothing under that key": status.Error(codes.NotFound, "key not found"),
		"no default environment": status.Error(codes.Unknown, "default environment not found"),
		"no such environment":    status.Error(codes.Unknown, "'dev': environment not found"),
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
		// Unknown is not absence on its own. azd passes any error carrying no
		// suggestion and no auth failure through untouched, so a failure to
		// load project state or the environment manager arrives under the same
		// code as "no default environment".
		"project state would not load": status.Error(codes.Unknown,
			"loading project state: open azure.yaml: permission denied"),
		"the environment manager broke": status.Error(codes.Unknown,
			"creating environment manager: no such host"),
		// The message is the only evidence, so it is matched whole. A failure
		// whose prose happens to mention one must not read as an absence.
		"a failure that mentions an environment": status.Error(codes.Unknown,
			"listing deployments: the environment not found in the subscription cache"),
		// status.FromError flattens a wrapper's own prose into the message it
		// reports, so a wrapper worded like an absence must not decide this.
		"a failure wrapped in absence-sounding prose": fmt.Errorf(
			"default environment not found in the cache: %w",
			status.Error(codes.Unknown, "loading project state: permission denied")),
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
