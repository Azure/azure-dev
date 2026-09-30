// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type betaEventTestServer struct {
	v1beta.UnimplementedEventServiceServer
	eventStream func(grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage]) error
}

func (s *betaEventTestServer) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	return s.eventStream(stream)
}

func newDemoBetaEventClient(
	t *testing.T,
	server *betaEventTestServer,
) (*azdext.AzdClient, func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	v1beta.RegisterEventServiceServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	cleanup := func() {
		client.Close()
		grpcServer.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			require.NoError(t, err)
		}
	}
	return client, cleanup
}

func TestDemoBetaEventRunner_WaitsForSubscriptionAcknowledgements(t *testing.T) {
	predeployReceived := make(chan struct{})
	postdeployReceived := make(chan struct{})
	allowPredeployAck := make(chan struct{})
	allowPostdeployAck := make(chan struct{})

	server := &betaEventTestServer{
		eventStream: func(
			stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
		) error {
			for _, event := range []struct {
				name     string
				received chan struct{}
				allowAck chan struct{}
			}{
				{"predeploy", predeployReceived, allowPredeployAck},
				{"postdeploy", postdeployReceived, allowPostdeployAck},
			} {
				request, err := stream.Recv()
				if err != nil {
					return err
				}
				if request.GetSubscribeProjectEvent() == nil ||
					len(request.GetSubscribeProjectEvent().GetEventNames()) != 1 ||
					request.GetSubscribeProjectEvent().GetEventNames()[0] != event.name {
					return fmt.Errorf("unexpected subscription for %s", event.name)
				}
				close(event.received)
				select {
				case <-event.allowAck:
				case <-stream.Context().Done():
					return stream.Context().Err()
				}
				if err := stream.Send(&v1beta.EventMessage{
					RequestId: request.GetRequestId(),
					MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
						SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
					},
				}); err != nil {
					return err
				}
			}
			<-stream.Context().Done()
			return nil
		},
	}
	client, cleanup := newDemoBetaEventClient(t, server)
	defer cleanup()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runner := newDemoBetaEventRunner(client, io.Discard)
	started := make(chan error, 1)
	go func() {
		started <- runner.Start(ctx)
	}()

	<-predeployReceived
	select {
	case err := <-started:
		t.Fatalf("runner returned before predeploy acknowledgement: %v", err)
	default:
	}
	close(allowPredeployAck)

	<-postdeployReceived
	select {
	case err := <-started:
		t.Fatalf("runner returned before postdeploy acknowledgement: %v", err)
	default:
	}
	close(allowPostdeployAck)
	require.NoError(t, <-started)

	runner.Close()
	require.NoError(t, runner.Wait())
}

func TestDemoBetaEventRunner_RejectsHostWithoutAcknowledgements(t *testing.T) {
	subscriptionReceived := make(chan struct{})
	server := &betaEventTestServer{
		eventStream: func(
			stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
		) error {
			if _, err := stream.Recv(); err != nil {
				return err
			}
			close(subscriptionReceived)
			<-stream.Context().Done()
			return nil
		},
	}
	client, cleanup := newDemoBetaEventClient(t, server)
	defer cleanup()

	runner := newDemoBetaEventRunner(client, io.Discard)
	runner.ackTimeout = 25 * time.Millisecond
	err := runner.Start(t.Context())
	require.ErrorContains(t, err, "timed out waiting for the predeploy beta subscription acknowledgement")
	<-subscriptionReceived
	require.NoError(t, runner.Wait())
}

func TestDemoBetaEventRunner_WaitsForActiveHandlerOnClose(t *testing.T) {
	server := &betaEventTestServer{
		eventStream: func(
			stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
		) error {
			for range 2 {
				request, err := stream.Recv()
				if err != nil {
					return err
				}
				if err := stream.Send(&v1beta.EventMessage{
					RequestId: request.GetRequestId(),
					MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
						SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
					},
				}); err != nil {
					return err
				}
			}
			if err := stream.Send(&v1beta.EventMessage{
				RequestId: "invocation-1",
				MessageType: &v1beta.EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &v1beta.InvokeProjectHandler{
						EventName: "predeploy",
					},
				},
			}); err != nil {
				return err
			}
			<-stream.Context().Done()
			return stream.Context().Err()
		},
	}
	client, cleanup := newDemoBetaEventClient(t, server)
	defer cleanup()

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	defer releaseWriter()

	runner := newDemoBetaEventRunner(client, blockingBetaOutputWriter{
		entered: entered,
		release: release,
	})
	require.NoError(t, runner.Start(t.Context()))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not begin writing output")
	}

	runner.Close()
	waited := make(chan error, 1)
	go func() {
		waited <- runner.Wait()
	}()
	select {
	case err := <-waited:
		releaseWriter()
		t.Fatalf("runner returned before its handler completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	releaseWriter()
	require.ErrorIs(t, <-waited, context.Canceled)
}

func TestBetaDeployOutputWriter_SplitsAtUTF8Boundaries(t *testing.T) {
	stream := &recordingBetaEventClientStream{ctx: t.Context()}
	var local bytes.Buffer
	runner := &demoBetaEventRunner{
		ctx:    t.Context(),
		stream: stream,
	}
	writer := &betaDeployOutputWriter{
		ctx:       t.Context(),
		runner:    runner,
		requestID: "invocation-1",
		local:     &local,
	}

	input := strings.Repeat("x", betaDeployOutputChunkBytes-1) +
		"💩" +
		strings.Repeat("y", betaDeployOutputChunkBytes+5)
	n, err := writer.Write([]byte(input))
	require.NoError(t, err)
	require.Equal(t, len(input), n)
	require.Equal(t, input, local.String())
	require.Greater(t, len(stream.sent), 1)

	var joined strings.Builder
	for _, message := range stream.sent {
		chunk := message.GetHandlerOutput().GetOutput()
		require.NotEmpty(t, chunk)
		require.True(t, utf8.ValidString(chunk))
		require.LessOrEqual(t, len(chunk), betaDeployOutputChunkBytes)
		require.Equal(t, "invocation-1", message.GetRequestId())
		joined.WriteString(chunk)
	}
	require.Equal(t, input, joined.String())
}

func TestBetaDeployOutputWriter_PreservesLocalBytesAndNormalizesWireText(t *testing.T) {
	stream := &recordingBetaEventClientStream{ctx: t.Context()}
	var local bytes.Buffer
	writer := &betaDeployOutputWriter{
		ctx:       t.Context(),
		runner:    &demoBetaEventRunner{ctx: t.Context(), stream: stream},
		requestID: "invocation-1",
		local:     &local,
	}

	input := []byte{0xff, 'a'}
	n, err := writer.Write(input)
	require.NoError(t, err)
	require.Equal(t, len(input), n)
	require.Equal(t, input, local.Bytes())
	require.Equal(t, "\uFFFDa", stream.sent[0].GetHandlerOutput().GetOutput())
}

func TestBetaDeployOutputWriter_ReturnsWriteAndSendErrors(t *testing.T) {
	t.Run("short local write", func(t *testing.T) {
		stream := &recordingBetaEventClientStream{ctx: t.Context()}
		writer := &betaDeployOutputWriter{
			ctx:       t.Context(),
			runner:    &demoBetaEventRunner{ctx: t.Context(), stream: stream},
			requestID: "invocation-1",
			local:     shortBetaOutputWriter{},
		}

		n, err := writer.Write([]byte("warning"))
		require.Equal(t, 1, n)
		require.ErrorIs(t, err, io.ErrShortWrite)
		require.Empty(t, stream.sent)
	})

	t.Run("stream send error", func(t *testing.T) {
		sendErr := errors.New("stream send failed")
		stream := &recordingBetaEventClientStream{ctx: t.Context(), sendErr: sendErr}
		var local bytes.Buffer
		writer := &betaDeployOutputWriter{
			ctx:       t.Context(),
			runner:    &demoBetaEventRunner{ctx: t.Context(), stream: stream},
			requestID: "invocation-1",
			local:     &local,
		}

		n, err := writer.Write([]byte("warning"))
		require.Equal(t, len("warning"), n)
		require.ErrorIs(t, err, sendErr)
		require.Equal(t, "warning", local.String())
	})

	t.Run("closed writer", func(t *testing.T) {
		writer := &betaDeployOutputWriter{ctx: t.Context()}
		writer.Close()
		_, err := writer.Write([]byte("warning"))
		require.ErrorContains(t, err, "writer is closed")
	})
}

func TestWrapDemoBetaError_PreservesErrorChainPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cause error
	}{
		{
			name: "local error inside tool error",
			cause: &azdext.LocalError{
				Message:    "invalid configuration",
				Code:       "invalid_config",
				Category:   azdext.LocalErrorCategoryValidation,
				CauseTypes: []string{"*demo.ConfigError"},
				Suggestion: "Check the configuration",
				Links:      []errorhandler.ErrorLink{{URL: "https://example.com/config", Title: "Configuration"}},
			},
		},
		{
			name: "service error inside tool error",
			cause: &azdext.ServiceError{
				Message:     "service unavailable",
				ErrorCode:   "Unavailable",
				StatusCode:  503,
				ServiceName: "example.com",
				Suggestion:  "Try again later",
				Links:       []errorhandler.ErrorLink{{URL: "https://example.com/status", Title: "Service status"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := &azdext.ToolError{
				Message:    "tool failed",
				Err:        tt.cause,
				ToolName:   "docker",
				Kind:       azdext.ToolErrorKindFailed,
				ExitCode:   new(42),
				Suggestion: "Check the tool output",
			}
			message := wrapDemoBetaError(err)
			expected := wrapDemoBetaError(tt.cause)
			require.Equal(t, expected.GetOrigin(), message.GetOrigin())
			require.Equal(t, expected.GetSource(), message.GetSource())
			require.Equal(t, expected.GetMessage(), message.GetMessage())
			require.Equal(t, expected.GetSuggestion(), message.GetSuggestion())
			require.Equal(t, expected.GetLinks(), message.GetLinks())
			require.Nil(t, message.GetToolError())
		})
	}

	t.Run("tool details without structured cause", func(t *testing.T) {
		message := wrapDemoBetaError(&azdext.ToolError{
			Message:    "tool failed",
			Err:        errors.New("process exited"),
			ToolName:   "docker",
			Kind:       azdext.ToolErrorKindFailed,
			ExitCode:   new(42),
			Suggestion: "Check the tool output",
		})
		require.Equal(t, v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL, message.GetOrigin())
		require.Equal(t, "tool failed", message.GetMessage())
		require.Equal(t, "Check the tool output", message.GetSuggestion())
		require.Equal(t, "docker", message.GetToolError().GetToolName())
		require.Equal(t, "failed", message.GetToolError().GetFailureKind())
		require.Equal(t, int64(42), message.GetToolError().GetExitCode())
	})
}

type recordingBetaEventClientStream struct {
	ctx     context.Context
	sent    []*v1beta.EventMessage
	sendErr error
}

func (s *recordingBetaEventClientStream) Send(message *v1beta.EventMessage) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, message)
	return nil
}

func (*recordingBetaEventClientStream) Recv() (*v1beta.EventMessage, error) {
	return nil, io.EOF
}

func (*recordingBetaEventClientStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (*recordingBetaEventClientStream) Trailer() metadata.MD {
	return metadata.MD{}
}

func (*recordingBetaEventClientStream) CloseSend() error {
	return nil
}

func (s *recordingBetaEventClientStream) Context() context.Context {
	return s.ctx
}

func (*recordingBetaEventClientStream) SendMsg(any) error {
	return nil
}

func (*recordingBetaEventClientStream) RecvMsg(any) error {
	return nil
}

type shortBetaOutputWriter struct{}

func (shortBetaOutputWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return 1, nil
}

type blockingBetaOutputWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w blockingBetaOutputWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(data), nil
}
