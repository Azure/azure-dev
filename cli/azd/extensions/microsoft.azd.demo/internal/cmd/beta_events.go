// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

const (
	betaDeploySubscriptionTimeout = 5 * time.Second
	betaDeployOutputChunkBytes    = 32 * 1024
)

type demoBetaEventRunner struct {
	client     *azdext.AzdClient
	output     io.Writer
	ackTimeout time.Duration

	mu          sync.Mutex
	pending     map[string]chan *v1beta.EventMessage
	active      map[string]struct{}
	terminalErr error
	sendMu      sync.Mutex
	outputMu    sync.Mutex
	invocations sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
	stream grpc.BidiStreamingClient[v1beta.EventMessage, v1beta.EventMessage]
	done   chan struct{}
}

func newDemoBetaEventRunner(client *azdext.AzdClient, output io.Writer) *demoBetaEventRunner {
	return &demoBetaEventRunner{
		client:     client,
		output:     output,
		ackTimeout: betaDeploySubscriptionTimeout,
		pending:    map[string]chan *v1beta.EventMessage{},
		active:     map[string]struct{}{},
	}
}

func (r *demoBetaEventRunner) Start(ctx context.Context) error {
	r.done = make(chan struct{})
	if r.client == nil {
		err := errors.New("azd client is required")
		r.setTerminalError(err)
		close(r.done)
		return err
	}
	if r.output == nil {
		err := errors.New("deploy hook output writer is required")
		r.setTerminalError(err)
		close(r.done)
		return err
	}

	r.ctx, r.cancel = context.WithCancel(ctx)
	stream, err := r.client.EventsBeta().EventStream(r.ctx)
	if err != nil {
		err = fmt.Errorf("open beta event stream: %w", err)
		r.setTerminalError(err)
		r.cancel()
		close(r.done)
		return err
	}
	r.stream = stream
	go r.receiveLoop()

	ackTimeout := r.ackTimeout
	if ackTimeout <= 0 {
		ackTimeout = betaDeploySubscriptionTimeout
	}
	ackCtx, cancel := context.WithTimeout(r.ctx, ackTimeout)
	defer cancel()
	for _, eventName := range []string{"predeploy", "postdeploy"} {
		if err := r.subscribeProjectEvent(ackCtx, eventName); err != nil {
			r.stop()
			_ = r.Wait()
			return err
		}
	}
	return nil
}

func (r *demoBetaEventRunner) Done() <-chan struct{} {
	return r.done
}

func (r *demoBetaEventRunner) Wait() error {
	if r.done == nil {
		return errors.New("beta event runner has not started")
	}
	<-r.done
	r.invocations.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminalErr
}

func (r *demoBetaEventRunner) Close() {
	r.stop()
}

func (r *demoBetaEventRunner) stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *demoBetaEventRunner) subscribeProjectEvent(
	ctx context.Context,
	eventName string,
) error {
	requestID := uuid.NewString()
	response := make(chan *v1beta.EventMessage, 1)
	r.mu.Lock()
	r.pending[requestID] = response
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}()

	request := &v1beta.EventMessage{
		RequestId: requestID,
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{eventName},
			},
		},
	}
	if err := r.send(request); err != nil {
		return fmt.Errorf("send %s subscription: %w", eventName, err)
	}

	select {
	case message := <-response:
		if eventErr := message.GetError(); eventErr != nil {
			return fmt.Errorf("azd rejected %s subscription: %s", eventName, eventErr.GetMessage())
		}
		if message.GetRequestId() != requestID ||
			message.GetSubscribeProjectEventResponse() == nil {
			return fmt.Errorf("azd sent an invalid acknowledgement for %s", eventName)
		}
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf(
				"timed out waiting for the %s beta subscription acknowledgement; "+
					"use an azd build that supports beta deploy hooks: %w",
				eventName,
				ctx.Err(),
			)
		}
		return ctx.Err()
	case <-r.done:
		return fmt.Errorf("beta event stream stopped before %s was acknowledged: %w", eventName, r.Wait())
	}
}

func (r *demoBetaEventRunner) send(message *v1beta.EventMessage) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if err := r.stream.Send(message); err != nil {
		return err
	}
	return nil
}

func (r *demoBetaEventRunner) receiveLoop() {
	defer close(r.done)
	for {
		message, err := r.stream.Recv()
		if err != nil {
			if r.ctx.Err() == nil {
				if errors.Is(err, io.EOF) {
					r.setTerminalError(errors.New("beta event stream closed unexpectedly"))
				} else {
					r.setTerminalError(fmt.Errorf("receive beta event: %w", err))
				}
			}
			return
		}
		if err := r.dispatch(message); err != nil {
			r.setTerminalError(err)
			r.stop()
			return
		}
	}
}

func (r *demoBetaEventRunner) dispatch(message *v1beta.EventMessage) error {
	if message == nil || message.GetRequestId() == "" {
		return errors.New("azd sent a beta event message without a request_id")
	}

	switch message.GetMessageType().(type) {
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return r.deliverSubscriptionResponse(message)
	case *v1beta.EventMessage_InvokeProjectHandler:
		if message.GetInvokeProjectHandler() == nil {
			return errors.New("azd sent an empty project handler invocation")
		}
		if err := r.beginInvocation(message.GetRequestId()); err != nil {
			return err
		}
		r.invocations.Go(func() {
			r.handleProjectInvocation(message)
		})
		return nil
	default:
		if message.GetError() != nil {
			return r.deliverSubscriptionResponse(message)
		}
		return fmt.Errorf("azd sent an unexpected beta event message %T", message.GetMessageType())
	}
}

func (r *demoBetaEventRunner) deliverSubscriptionResponse(message *v1beta.EventMessage) error {
	r.mu.Lock()
	response := r.pending[message.GetRequestId()]
	r.mu.Unlock()
	if response == nil {
		return fmt.Errorf("azd sent an unsolicited subscription response %q", message.GetRequestId())
	}
	select {
	case response <- message:
		return nil
	default:
		return fmt.Errorf("azd sent a duplicate subscription response %q", message.GetRequestId())
	}
}

func (r *demoBetaEventRunner) beginInvocation(requestID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.active[requestID]; exists {
		return fmt.Errorf("azd reused active deploy hook request_id %q", requestID)
	}
	r.active[requestID] = struct{}{}
	return nil
}

func (r *demoBetaEventRunner) handleProjectInvocation(message *v1beta.EventMessage) {
	request := message.GetInvokeProjectHandler()
	requestID := message.GetRequestId()
	defer func() {
		r.mu.Lock()
		delete(r.active, requestID)
		r.mu.Unlock()
	}()

	output := &betaDeployOutputWriter{
		ctx:       r.ctx,
		runner:    r,
		requestID: requestID,
		local:     r.output,
	}
	err := r.runProjectHook(request, output)
	output.Close()

	status := "completed"
	var structuredError *v1beta.ExtensionError
	messageText := ""
	if err != nil {
		status = "failed"
		messageText = err.Error()
		structuredError = wrapDemoBetaError(err)
	}

	response := &v1beta.EventMessage{
		RequestId: requestID,
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: request.GetEventName(),
				Status:    status,
				Message:   messageText,
				Error:     structuredError,
			},
		},
	}
	if err := r.send(response); err != nil {
		r.setTerminalError(fmt.Errorf("send deploy hook status: %w", err))
		r.stop()
	}
}

func (r *demoBetaEventRunner) runProjectHook(
	request *v1beta.InvokeProjectHandler,
	output io.Writer,
) error {
	switch request.GetEventName() {
	case "predeploy", "postdeploy":
		return runDemoWork(r.ctx, func(index int) error {
			_, err := fmt.Fprintf(
				output,
				"%d. Doing important %s project work in extension...\n",
				index,
				request.GetEventName(),
			)
			return err
		})
	default:
		return fmt.Errorf("no beta project handler registered for %q", request.GetEventName())
	}
}

func (r *demoBetaEventRunner) setTerminalError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalErr == nil {
		r.terminalErr = err
	}
}

type betaDeployOutputWriter struct {
	ctx       context.Context
	runner    *demoBetaEventRunner
	requestID string
	local     io.Writer

	mu     sync.Mutex
	closed bool
}

func (w *betaDeployOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, errors.New("deploy hook output writer is closed")
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}

	w.runner.outputMu.Lock()
	written, err := w.local.Write(data)
	w.runner.outputMu.Unlock()
	if written < 0 || written > len(data) {
		return 0, errors.New("deploy hook output writer returned an invalid byte count")
	}
	if err != nil {
		return written, err
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}

	text := strings.ToValidUTF8(string(data), "\uFFFD")
	for len(text) > 0 {
		chunk, rest := splitUTF8Chunk(text, betaDeployOutputChunkBytes)
		if chunk == "" {
			return written, errors.New("could not split deploy hook output on a UTF-8 boundary")
		}
		message := &v1beta.EventMessage{
			RequestId: w.requestID,
			MessageType: &v1beta.EventMessage_HandlerOutput{
				HandlerOutput: &v1beta.HandlerOutput{Output: chunk},
			},
		}
		if err := w.runner.send(message); err != nil {
			return written, fmt.Errorf("send deploy hook output: %w", err)
		}
		text = rest
	}
	return written, nil
}

func (w *betaDeployOutputWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

func splitUTF8Chunk(text string, limit int) (string, string) {
	if len(text) <= limit {
		return text, ""
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], text[end:]
}

func wrapDemoBetaError(err error) *v1beta.ExtensionError {
	stableError := azdext.WrapError(err)
	betaError := &v1beta.ExtensionError{
		Message:    stableError.GetMessage(),
		Origin:     v1beta.ErrorOrigin(stableError.GetOrigin()),
		Suggestion: stableError.GetSuggestion(),
	}
	for _, link := range stableError.GetLinks() {
		if link != nil {
			betaError.Links = append(betaError.Links, &v1beta.ErrorLink{
				Url:   link.GetUrl(),
				Title: link.GetTitle(),
			})
		}
	}

	switch source := stableError.GetSource().(type) {
	case *azdext.ExtensionError_ServiceError:
		if detail := source.ServiceError; detail != nil {
			betaError.Source = &v1beta.ExtensionError_ServiceError{
				ServiceError: &v1beta.ServiceErrorDetail{
					ErrorCode:   detail.GetErrorCode(),
					StatusCode:  detail.GetStatusCode(),
					ServiceName: detail.GetServiceName(),
				},
			}
		}
	case *azdext.ExtensionError_LocalError:
		if detail := source.LocalError; detail != nil {
			betaDetail := &v1beta.LocalErrorDetail{
				Code:     detail.GetCode(),
				Category: detail.GetCategory(),
			}
			if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
				betaDetail.CauseTypes = errorchain.NormalizeCauseTypes(localErr.CauseTypes)
			}
			betaError.Source = &v1beta.ExtensionError_LocalError{LocalError: betaDetail}
		}
	}

	if toolErr, ok := errors.AsType[*azdext.ToolError](err); ok {
		betaError.Origin = v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL
		detail := &v1beta.ToolErrorDetail{
			ToolName:    toolErr.ToolName,
			FailureKind: string(toolErr.Kind),
		}
		if toolErr.ExitCode != nil {
			exitCode := int64(*toolErr.ExitCode)
			detail.ExitCode = &exitCode
		}
		betaError.Source = &v1beta.ExtensionError_ToolError{ToolError: detail}
	}
	return betaError
}
