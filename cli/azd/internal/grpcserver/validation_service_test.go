// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/require"
)

// validationTestStream simulates a bidirectional gRPC stream for ValidationMessage.
type validationTestStream struct {
	serverToClient chan *azdext.ValidationMessage
	clientToServer chan *azdext.ValidationMessage
	closed         bool
	mu             sync.Mutex
}

func newValidationTestStream() *validationTestStream {
	return &validationTestStream{
		serverToClient: make(chan *azdext.ValidationMessage, 100),
		clientToServer: make(chan *azdext.ValidationMessage, 100),
	}
}

func (s *validationTestStream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.serverToClient)
		close(s.clientToServer)
	}
}

// serverSide returns a BidiStream where Send goes to client, Recv comes from client.
func (s *validationTestStream) serverSide() grpcbroker.BidiStream[azdext.ValidationMessage] {
	return &valStreamSide{send: s.serverToClient, recv: s.clientToServer, parent: s}
}

// clientSide returns a BidiStream where Send goes to server, Recv comes from server.
func (s *validationTestStream) clientSide() grpcbroker.BidiStream[azdext.ValidationMessage] {
	return &valStreamSide{send: s.clientToServer, recv: s.serverToClient, parent: s}
}

type valStreamSide struct {
	send   chan *azdext.ValidationMessage
	recv   chan *azdext.ValidationMessage
	parent *validationTestStream
}

func (v *valStreamSide) Send(msg *azdext.ValidationMessage) error {
	v.parent.mu.Lock()
	closed := v.parent.closed
	v.parent.mu.Unlock()
	if closed {
		return io.EOF
	}
	v.send <- msg
	return nil
}

func (v *valStreamSide) Recv() (*azdext.ValidationMessage, error) {
	msg, ok := <-v.recv
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func newTestValidationExtension() *extensions.Extension {
	return &extensions.Extension{
		Id:        "test.validation",
		Namespace: "test",
		Capabilities: []extensions.CapabilityType{
			extensions.ValidationProviderCapability,
		},
	}
}

func TestValidationService_DispatchChecks_NoChecks(t *testing.T) {
	svc := &ValidationService{}

	results, ruleIDs, err := svc.DispatchChecks(
		t.Context(), "local-preflight", nil,
	)
	require.NoError(t, err)
	require.Nil(t, results)
	require.Nil(t, ruleIDs)
}

func TestValidationService_OnRegisterRequest_Validations(t *testing.T) {
	svc := &ValidationService{}
	ext := newTestValidationExtension()

	tests := []struct {
		name      string
		checkType string
		ruleID    string
		wantErr   bool
	}{
		{"empty_check_type", "", "rule1", true},
		{"empty_rule_id", "local-preflight", "", true},
		{"whitespace_check_type", "  ", "rule1", true},
		{"whitespace_rule_id", "local-preflight", "  ", true},
		{"valid", "local-preflight", "rule1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var registered []validationCheckEntry
			mu := &sync.Mutex{}
			resp, err := svc.onRegisterRequest(
				t.Context(),
				&azdext.RegisterValidationCheckRequest{
					CheckType: tt.checkType,
					RuleId:    tt.ruleID,
				},
				ext,
				nil, // broker not needed for registration-only test
				&registered,
				mu,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
			}
		})
	}
}

func TestValidationService_OnRegisterRequest_DuplicateRejection(t *testing.T) {
	svc := &ValidationService{}
	ext := newTestValidationExtension()
	var registered []validationCheckEntry
	mu := &sync.Mutex{}

	// Register first time — should succeed
	resp, err := svc.onRegisterRequest(
		t.Context(),
		&azdext.RegisterValidationCheckRequest{
			CheckType: "local-preflight",
			RuleId:    "unique_rule",
		},
		ext,
		nil,
		&registered,
		mu,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Register same rule again — should fail with AlreadyExists
	_, err = svc.onRegisterRequest(
		t.Context(),
		&azdext.RegisterValidationCheckRequest{
			CheckType: "local-preflight",
			RuleId:    "unique_rule",
		},
		ext,
		nil,
		&registered,
		mu,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already registered")
}

func TestValidationService_DispatchChecks_NoMatchingType(t *testing.T) {
	svc := &ValidationService{}
	ext := newTestValidationExtension()

	// Register a check for "local-preflight"
	var registered []validationCheckEntry
	mu := &sync.Mutex{}
	_, _ = svc.onRegisterRequest(
		t.Context(),
		&azdext.RegisterValidationCheckRequest{
			CheckType: "local-preflight",
			RuleId:    "test_rule",
		},
		ext,
		nil,
		&registered,
		mu,
	)

	// Dispatch for a different check type — should find 0 matches
	results, ruleIDs, err := svc.DispatchChecks(
		t.Context(), "other-check-type", nil,
	)
	require.NoError(t, err)
	require.Nil(t, results)
	require.Nil(t, ruleIDs)
}

func TestValidationService_NewValidationService(t *testing.T) {
	mgr := &extensions.Manager{}
	svc := NewValidationService(mgr)
	require.NotNil(t, svc)
	require.Same(t, mgr, svc.extensionManager)
}

func TestSendContextChunks_EmptyContext(t *testing.T) {
	sim := newValidationTestStream()
	defer sim.Close()

	envelope := azdext.NewValidationEnvelope()
	broker := grpcbroker.NewMessageBroker(
		sim.serverSide(), envelope, "test", nil,
	)

	err := sendContextChunks(
		t.Context(), broker, "ctx-1", "local-preflight",
		map[string][]byte{},
	)
	require.NoError(t, err)
}

func TestSendContextChunks_SmallData(t *testing.T) {
	sim := newValidationTestStream()
	defer sim.Close()

	envelope := azdext.NewValidationEnvelope()
	broker := grpcbroker.NewMessageBroker(
		sim.serverSide(), envelope, "test", nil,
	)

	ctx, cancel := context.WithCancel(t.Context())

	// Goroutine reads from client side and responds with ack on final chunk
	go func() {
		for {
			msg, err := sim.clientSide().Recv()
			if err != nil {
				return
			}
			chunk := msg.GetPrepareValidationContextChunk()
			if chunk != nil && chunk.GetIsLastKey() {
				resp := &azdext.ValidationMessage{
					RequestId: msg.GetRequestId(),
					MessageType: &azdext.ValidationMessage_PrepareValidationContextResponse{
						PrepareValidationContextResponse: &azdext.PrepareValidationContextResponse{},
					},
				}
				if err := sim.clientSide().Send(resp); err != nil {
					return
				}
			}
		}
	}()

	// Start broker
	go func() {
		_ = broker.Run(ctx)
	}()
	_ = broker.Ready(ctx)

	contextData := map[string][]byte{
		"env_location": []byte("eastus2"),
		"arm_template": []byte(`{"resources":[]}`),
	}

	err := sendContextChunks(
		ctx, broker, "ctx-123", "local-preflight", contextData,
	)
	require.NoError(t, err)
	cancel()
}

func TestValidationService_DispatchChecks_WithBroker(t *testing.T) {
	sim := newValidationTestStream()
	defer sim.Close()

	envelope := azdext.NewValidationEnvelope()
	broker := grpcbroker.NewMessageBroker(
		sim.serverSide(), envelope, "test-ext", nil,
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Simulate extension side: respond to context chunks and check requests
	go func() {
		for {
			msg, err := sim.clientSide().Recv()
			if err != nil {
				return
			}
			// Handle context chunk — ack on final
			if chunk := msg.GetPrepareValidationContextChunk(); chunk != nil {
				if chunk.GetIsLastKey() {
					resp := &azdext.ValidationMessage{
						RequestId: msg.GetRequestId(),
						MessageType: &azdext.ValidationMessage_PrepareValidationContextResponse{
							PrepareValidationContextResponse: &azdext.PrepareValidationContextResponse{},
						},
					}
					_ = sim.clientSide().Send(resp)
				}
				continue
			}
			// Handle check request — return a warning
			if checkReq := msg.GetValidationCheckRequest(); checkReq != nil {
				resp := &azdext.ValidationMessage{
					RequestId: msg.GetRequestId(),
					MessageType: &azdext.ValidationMessage_ValidationCheckResponse{
						ValidationCheckResponse: &azdext.ValidationCheckResponse{
							Results: []*azdext.ValidationCheckResult{
								{
									Severity:     azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_WARNING,
									DiagnosticId: "test_diag",
									Message:      "test warning from extension",
								},
							},
						},
					},
				}
				_ = sim.clientSide().Send(resp)
				continue
			}
		}
	}()

	// Start broker
	go func() {
		_ = broker.Run(ctx)
	}()
	_ = broker.Ready(ctx)

	// Manually inject a check entry (simulating what onRegisterRequest would do)
	ext := newTestValidationExtension()
	svc := &ValidationService{}
	svc.checks = []validationCheckEntry{
		{
			CheckType: "local-preflight",
			RuleID:    "ext_rule_1",
			Extension: ext,
			Broker:    broker,
		},
		{
			CheckType: "local-preflight",
			RuleID:    "ext_rule_2",
			Extension: ext,
			Broker:    broker,
		},
	}

	contextData := map[string][]byte{
		"env_location": []byte("westus2"),
	}

	results, ruleIDs, err := svc.DispatchChecks(
		ctx, "local-preflight", contextData,
	)
	require.NoError(t, err)
	require.Len(t, results, 2, "should have 2 results (one per check)")
	require.Len(t, ruleIDs, 2)
	require.Contains(t, ruleIDs, "ext_rule_1")
	require.Contains(t, ruleIDs, "ext_rule_2")
	require.Equal(t, "test_diag", results[0].DiagnosticId)
}
