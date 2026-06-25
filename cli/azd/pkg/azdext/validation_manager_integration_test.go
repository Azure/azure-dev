// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeValidationServer is an in-process ValidationServiceServer used to drive
// the ValidationManager's Register/Receive/Ready flow over a real gRPC stream.
type fakeValidationServer struct {
	UnimplementedValidationServiceServer

	// registerErr, when set, causes the server to reply with an error envelope
	// on the registration response.
	registerErr error
	// dispatchOnRegister, when set, sends a validation check request and a
	// context chunk to the extension after the first registration completes.
	dispatchOnRegister bool
}

func (s *fakeValidationServer) Stream(
	stream grpc.BidiStreamingServer[ValidationMessage, ValidationMessage],
) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		// Respond to registration requests with a register response.
		if msg.GetRegisterValidationCheckRequest() != nil {
			resp := &ValidationMessage{
				RequestId: msg.GetRequestId(),
			}
			if s.registerErr != nil {
				resp.Error = &ExtensionError{Message: s.registerErr.Error()}
			} else {
				resp.MessageType = &ValidationMessage_RegisterValidationCheckResponse{
					RegisterValidationCheckResponse: &RegisterValidationCheckResponse{},
				}
			}
			if err := stream.Send(resp); err != nil {
				return err
			}

			if s.dispatchOnRegister && s.registerErr == nil {
				if err := s.dispatchCheck(stream, msg); err != nil {
					return err
				}
			}
		}
	}
}

// dispatchCheck sends a context chunk, waits for the extension's ack, then
// sends a validation check request, simulating what the core service does.
func (s *fakeValidationServer) dispatchCheck(
	stream grpc.BidiStreamingServer[ValidationMessage, ValidationMessage],
	registerMsg *ValidationMessage,
) error {
	checkType := registerMsg.GetRegisterValidationCheckRequest().GetCheckType()
	ruleID := registerMsg.GetRegisterValidationCheckRequest().GetRuleId()
	contextID := "ctx-integration"

	chunk := &ValidationMessage{
		RequestId: "chunk-1",
		MessageType: &ValidationMessage_PrepareValidationContextChunk{
			PrepareValidationContextChunk: &PrepareValidationContextChunk{
				ContextId:   contextID,
				CheckType:   checkType,
				Key:         "env_location",
				Data:        []byte("eastus2"),
				ChunkIndex:  0,
				IsLastChunk: true,
				IsLastKey:   true,
				TotalKeys:   1,
			},
		},
	}
	if err := stream.Send(chunk); err != nil {
		return err
	}

	// Wait for the extension to acknowledge the context before dispatching the check.
	for {
		ack, err := stream.Recv()
		if err != nil {
			return err
		}
		if ack.GetPrepareValidationContextResponse() != nil {
			break
		}
	}

	checkReq := &ValidationMessage{
		RequestId: "check-1",
		MessageType: &ValidationMessage_ValidationCheckRequest{
			ValidationCheckRequest: &ValidationCheckRequest{
				CheckType: checkType,
				RuleId:    ruleID,
				ContextId: contextID,
			},
		},
	}
	return stream.Send(checkReq)
}

// startFakeValidationServer spins up an in-process gRPC server backed by bufconn
// and returns a connected AzdClient plus a cleanup function.
func startFakeValidationServer(
	t *testing.T,
	srv *fakeValidationServer,
) *AzdClient {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	RegisterValidationServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	//nolint:staticcheck // grpc.DialContext with bufconn dialer is the standard test pattern
	conn, err := grpc.DialContext(
		t.Context(),
		"bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		conn.Close()
		grpcServer.Stop()
	})

	return &AzdClient{connection: conn}
}

func TestValidationManager_Register_Integration(t *testing.T) {
	client := startFakeValidationServer(t, &fakeValidationServer{})

	mgr := NewValidationManager("test.ext", client, nil)
	defer func() { _ = mgr.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Start the receiver loop (exercises Receive + ensureStream + registerHandlers).
	go func() {
		_ = mgr.Receive(ctx)
	}()

	// Wait for broker readiness (exercises Ready).
	require.NoError(t, mgr.Ready(ctx))

	// Register a check (exercises Register end-to-end over the stream).
	factory := func() ValidationCheckProvider { return &mockProvider{} }
	err := mgr.Register(ctx, factory, "local-preflight", "rule_1")
	require.NoError(t, err)

	// Registering the same rule again should fail locally (duplicate).
	err = mgr.Register(ctx, factory, "local-preflight", "rule_1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already registered")
}

func TestValidationManager_Register_ServerError(t *testing.T) {
	client := startFakeValidationServer(t, &fakeValidationServer{
		registerErr: errors.New("registration rejected by core"),
	})

	mgr := NewValidationManager("test.ext", client, nil)
	defer func() { _ = mgr.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	go func() {
		_ = mgr.Receive(ctx)
	}()
	require.NoError(t, mgr.Ready(ctx))

	factory := func() ValidationCheckProvider { return &mockProvider{} }
	err := mgr.Register(ctx, factory, "local-preflight", "rule_err")
	require.Error(t, err)
	require.Contains(t, err.Error(), "registration rejected by core")

	// The factory should have been rolled back so a retry path is clean.
	mgr.mu.RLock()
	_, exists := mgr.factories[validationCheckKey{CheckType: "local-preflight", RuleID: "rule_err"}]
	mgr.mu.RUnlock()
	require.False(t, exists, "factory should be removed after failed registration")
}

func TestValidationManager_Register_EmptyArgs(t *testing.T) {
	client := startFakeValidationServer(t, &fakeValidationServer{})
	mgr := NewValidationManager("test.ext", client, nil)
	defer func() { _ = mgr.Close() }()

	ctx := t.Context()
	factory := func() ValidationCheckProvider { return &mockProvider{} }

	err := mgr.Register(ctx, factory, "", "rule")
	require.Error(t, err)
	require.Contains(t, err.Error(), "check type cannot be empty")

	err = mgr.Register(ctx, factory, "local-preflight", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rule ID cannot be empty")
}

func TestValidationManager_Dispatch_Integration(t *testing.T) {
	// Server dispatches a context chunk + check request after registration.
	client := startFakeValidationServer(t, &fakeValidationServer{
		dispatchOnRegister: true,
	})

	mgr := NewValidationManager("test.ext", client, nil)
	defer func() { _ = mgr.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	go func() {
		_ = mgr.Receive(ctx)
	}()
	require.NoError(t, mgr.Ready(ctx))

	// Provider records the context it was invoked with.
	invoked := make(chan *ValidationContext, 1)
	factory := func() ValidationCheckProvider {
		return &recordingProvider{invoked: invoked}
	}
	require.NoError(t, mgr.Register(ctx, factory, "local-preflight", "rule_dispatch"))

	// The server pushes a check request; the manager should invoke the provider.
	select {
	case valCtx := <-invoked:
		require.Equal(t, "ctx-integration", valCtx.ContextID)
		loc, ok := valCtx.EnvLocation()
		require.True(t, ok)
		require.Equal(t, "eastus2", loc)
	case <-ctx.Done():
		t.Fatal("provider was not invoked before timeout")
	}
}

// recordingProvider captures the context it receives for assertions.
type recordingProvider struct {
	invoked chan *ValidationContext
}

func (p *recordingProvider) Validate(
	_ context.Context,
	valCtx *ValidationContext,
	_ *ValidationCheckRequest,
) (*ValidationCheckResponse, error) {
	p.invoked <- valCtx
	return &ValidationCheckResponse{}, nil
}
