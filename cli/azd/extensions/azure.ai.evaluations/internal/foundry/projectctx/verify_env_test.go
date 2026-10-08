// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// lookupStub answers the one call that confirms an environment exists.
type lookupStub struct {
	err   error
	asked []string
}

func (l *lookupStub) Get(
	_ context.Context, req *azdext.GetEnvironmentRequest, _ ...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	l.asked = append(l.asked, req.Name)
	if l.err != nil {
		return nil, l.err
	}
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: req.Name},
	}, nil
}

// The named-environment check used to live inside the endpoint cascade, which
// --project-endpoint skips entirely: `run start -e typo --project-endpoint ...`
// was accepted while the same command without the flag was refused. The name
// decides where every id and version is read from and written to, whichever
// level supplied the endpoint, so the check does not belong to any level.
func TestAnEnvironmentAzdDoesNotHaveIsRefused(t *testing.T) {
	stub := &lookupStub{
		err: status.Error(codes.Unknown, "'typo': environment not found"),
	}

	err := verifyEnvironment(context.Background(), stub, "typo")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo")
	assert.Equal(t, []string{"typo"}, stub.asked)
}

func TestAnEnvironmentAzdHasIsAccepted(t *testing.T) {
	stub := &lookupStub{}

	require.NoError(t, verifyEnvironment(context.Background(), stub, "staging"))
	assert.Equal(t, []string{"staging"}, stub.asked)
}

// A daemon that could not answer has not said the environment is missing.
// Refusing there would turn a hiccup into "your environment does not exist";
// the commands that need azd report their own failures.
func TestAFailureThatIsNotAnAnswerDoesNotRefuse(t *testing.T) {
	for _, err := range []error{
		status.Error(codes.Internal, "the store is on fire"),
		status.Error(codes.Unavailable, "no daemon"),
		status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`"),
	} {
		stub := &lookupStub{err: err}
		assert.NoError(t, verifyEnvironment(context.Background(), stub, "staging"),
			"unexpected refusal for %v", err)
	}
}

// Nothing named means azd's default, which needs no confirming.
func TestNoSelectionAsksNothing(t *testing.T) {
	require.NoError(t, VerifySelectedEnvironment(context.Background()))
}
