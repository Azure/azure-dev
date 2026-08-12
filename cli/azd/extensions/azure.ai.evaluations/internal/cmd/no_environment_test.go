// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
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
// The sentinel is wrapped in a gRPC status on the way out of azd, so the text
// is what there is to match on. These are the real shapes.
func TestNoDefaultEnvironmentIsToldApartFromAFailureToAsk(t *testing.T) {
	// The text of azd's environment.ErrDefaultEnvironmentNotFound.
	const azdText = "default environment not found"

	assert.True(t, isNoDefaultEnvironmentError(errors.New(azdText)),
		"the sentinel's own text")
	assert.True(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unknown, azdText)),
		"and the same thing after azd wraps it in a gRPC status")
	assert.True(t,
		isNoDefaultEnvironmentError(fmt.Errorf("getting environment: %w", errors.New(azdText))),
		"and wrapped again by a caller")

	assert.False(t,
		isNoDefaultEnvironmentError(status.Error(codes.Unavailable, "connection refused")),
		"azd being unreachable is not an answer about environments")
	assert.False(t,
		isNoDefaultEnvironmentError(status.Error(codes.DeadlineExceeded, "context deadline exceeded")),
		"nor is a timeout")
	assert.False(t, isNoDefaultEnvironmentError(nil), "nor is success")
}
