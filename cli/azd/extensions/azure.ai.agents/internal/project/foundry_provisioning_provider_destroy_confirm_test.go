// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// destroyConfirmStubPromptServer is a PromptService stub that answers Confirm
// with a configured value (or error). It records how many times Confirm was
// called so tests can assert the provider actually prompted.
type destroyConfirmStubPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	confirmValue bool
	confirmErr   error
	confirmN     int
	lastMessage  string
}

func (s *destroyConfirmStubPromptServer) Confirm(
	_ context.Context, req *azdext.ConfirmRequest,
) (*azdext.ConfirmResponse, error) {
	s.confirmN++
	if req.GetOptions() != nil {
		s.lastMessage = req.GetOptions().GetMessage()
	}
	if s.confirmErr != nil {
		return nil, s.confirmErr
	}
	v := s.confirmValue
	return &azdext.ConfirmResponse{Value: &v}, nil
}

// newDestroyConfirmTestClient spins up a real gRPC server exposing the given
// prompt stub and returns an AzdClient connected to it, exercising the actual
// Confirm wire protocol between the extension and the azd host.
func newDestroyConfirmTestClient(
	t *testing.T,
	promptSrv azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterPromptServiceServer(srv, promptSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// TestDestroy_PromptDeclineReturnsCancelled drives the full Destroy path over a
// real gRPC connection: without --force the provider prompts, the user declines,
// and Destroy returns a clean cancellation without touching Azure.
func TestDestroy_PromptDeclineReturnsCancelled(t *testing.T) {
	prompt := &destroyConfirmStubPromptServer{confirmValue: false}
	client := newDestroyConfirmTestClient(t, prompt)

	p := &FoundryProvisioningProvider{azdClient: client, rgName: "rg-foundry-test"}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: false}, func(string) {})

	require.Error(t, err)
	assert.Equal(t, 1, prompt.confirmN, "expected exactly one confirmation prompt")
	assert.Contains(t, prompt.lastMessage, "rg-foundry-test",
		"prompt must name the resource group being deleted")

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeCancelled, local.Code)
}

// TestDestroy_PromptRequiredFallsBackToForce verifies that under `--no-prompt`
// (the host returns a "prompt required" error) Destroy surfaces the actionable
// --force guidance instead of proceeding, keeping CI/scripts deterministic.
func TestDestroy_PromptRequiredFallsBackToForce(t *testing.T) {
	prompt := &destroyConfirmStubPromptServer{
		confirmErr: status.Error(codes.FailedPrecondition, "prompt required: no terminal"),
	}
	client := newDestroyConfirmTestClient(t, prompt)

	p := &FoundryProvisioningProvider{azdClient: client, rgName: "rg-foundry-test"}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: false}, func(string) {})

	require.Error(t, err)
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeDestroyRequiresForce, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
	assert.Contains(t, local.Message, "rg-foundry-test")
	assert.Contains(t, local.Suggestion, "--force")
}

// TestDestroy_PromptCancelledReturnsCancelled verifies that a user cancellation
// (Ctrl-C, surfaced as a gRPC Canceled status) is reported as a clean
// cancellation rather than a hard error.
func TestDestroy_PromptCancelledReturnsCancelled(t *testing.T) {
	prompt := &destroyConfirmStubPromptServer{
		confirmErr: status.Error(codes.Canceled, "user cancelled"),
	}
	client := newDestroyConfirmTestClient(t, prompt)

	p := &FoundryProvisioningProvider{azdClient: client, rgName: "rg-foundry-test"}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: false}, func(string) {})

	require.Error(t, err)
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeCancelled, local.Code)
}

// TestConfirmDestroy_AcceptReturnsTrue verifies the accept path over real gRPC:
// when the user confirms, confirmDestroy reports true so Destroy proceeds.
func TestConfirmDestroy_AcceptReturnsTrue(t *testing.T) {
	prompt := &destroyConfirmStubPromptServer{confirmValue: true}
	client := newDestroyConfirmTestClient(t, prompt)

	p := &FoundryProvisioningProvider{azdClient: client, rgName: "rg-foundry-test"}
	confirmed, err := p.confirmDestroy(t.Context())

	require.NoError(t, err)
	assert.True(t, confirmed)
	assert.Equal(t, 1, prompt.confirmN)
}
