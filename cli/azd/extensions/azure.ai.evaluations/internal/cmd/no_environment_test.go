// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// azd reports "there is no environment" as an ERROR, not as an empty answer:
//
//	if defaultEnvironment == "" {
//	    return nil, environment.ErrDefaultEnvironmentNotFound
//	}
//
// So a check that treats every error as "could not ask" can never conclude
// there is no environment, and the diagnostic that names `azd env new` is
// unreachable -- which is what the first version of this did. Equally, treating
// every error as "no environment" would tell someone whose azd hiccupped to
// create an environment they already have.
//
// The only caller reads these off a gRPC call, so every error carries a status.
// These are the shapes that reach it.
func TestNoDefaultEnvironmentIsToldApartFromAFailureToAsk(t *testing.T) {
	// The text of azd's environment.ErrDefaultEnvironmentNotFound.
	const azdText = "default environment not found"

	assert.True(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unknown, azdText)),
		"the sentinel, as azd wraps it in a gRPC status")
	assert.True(t,
		isNoDefaultEnvironmentError(fmt.Errorf("getting environment: %w",
			status.Error(codes.Unknown, azdText))),
		"and wrapped again by a caller")
	// Outside a project there is nowhere an id could have been recorded, which
	// is the same answer for this caller. Missing it told anyone running
	// standalone to publish an eval that may already exist.
	assert.True(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unknown,
			"no project exists; to create a new project, run `azd init`")),
		"outside a project there is no environment either")

	assert.False(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unavailable, "connection refused")),
		"azd being unreachable is not an answer about environments")
	assert.False(t,
		isNoDefaultEnvironmentError(status.Error(codes.DeadlineExceeded, "context deadline exceeded")),
		"nor is a timeout")
	assert.False(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unknown, "loading project state: permission denied")),
		"nor is a daemon that broke while looking")
	assert.False(t, isNoDefaultEnvironmentError(nil), "nor is success")
}
