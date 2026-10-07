// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcbroker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/syncmap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrResourceExhausted is returned when a gRPC message exceeds the size limit.
var ErrResourceExhausted = errors.New("gRPC message size limit exceeded")

// wrapResourceExhausted detects gRPC ResourceExhausted errors (message too large)
// and wraps them with a clear message so callers can identify the root cause.
// Without this, oversized messages silently kill the broker stream and surface as
// confusing "channel closed by broker" errors far from the root cause.
func wrapResourceExhausted(err error, operation string) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.ResourceExhausted {
		return fmt.Errorf("%w: %s failed: "+
			"the message is larger than the default 4 MB max. "+
			"Enable gzip compression (grpc.UseCompressor(\"gzip\") call option) "+
			"on both client and server, or reduce the payload size: %w",
			ErrResourceExhausted, operation, err,
		)
	}
	return err
}

// ProgressFunc is a callback function for sending progress updates during handler execution
type ProgressFunc func(message string)

// MessageEnvelope provides broker-specific operations on message types.
// This is a stateless service that knows how to extract and manipulate message fields.
// The methods work with pointers (*T) to avoid copying and to match gRPC's pointer-based APIs.
// Context is provided to allow envelopes to extract metadata (e.g., extension claims from gRPC context).
type MessageEnvelope[T any] interface {
	// GetRequestId extracts or generates the request/correlation ID from a message.
	// For messages with a RequestId field, this returns that field.
	// For messages without RequestId, this can generate a correlation key from message content and context.
	GetRequestId(ctx context.Context, msg *T) string

	// SetRequestId sets the request ID on a message.
	// For messages without a RequestId field, this can be a no-op.
	SetRequestId(ctx context.Context, msg *T, id string)

	// GetError extracts the error from a message, if any
	GetError(msg *T) error

	// SetError sets an error on a message
	SetError(msg *T, err error)

	// GetInnerMessage extracts the inner message from the envelope's oneof field
	GetInnerMessage(msg *T) any

	// IsProgressMessage returns true if the message is a progress message
	IsProgressMessage(msg *T) bool

	// GetProgressMessage extracts the progress message text from a progress message.
	// Returns empty string if the message is not a progress message.
	GetProgressMessage(msg *T) string

	// CreateProgressMessage creates a new progress message envelope with the given text.
	// This is used by server-side handlers to send progress updates back to clients.
	CreateProgressMessage(requestId string, message string) *T
}

// CancellationMessageEnvelope customizes cancellation control messages for envelopes
// that cannot use the default request-id + error representation.
type CancellationMessageEnvelope[T any] interface {
	CreateCancellationMessage(ctx context.Context, request *T, err error) *T
	IsCancellationMessage(ctx context.Context, msg *T) bool
	GetCancellationError(msg *T) error
}

// PersistentHandlerContextEnvelope identifies messages whose handlers retain
// the stream context after returning, such as provider registrations and event
// subscriptions. These handlers must not receive a request-scoped context that
// is canceled as soon as the registration call completes.
type PersistentHandlerContextEnvelope[T any] interface {
	PreserveHandlerContext(ctx context.Context, msg *T) bool
}

const defaultCancellationGracePeriod = time.Second

var errMessageBrokerClosed = errors.New("message broker closed")
var errRequestAlreadyPending = errors.New("request with correlation id already pending")

type messageBrokerOptions struct {
	cancellationGracePeriod time.Duration
}

// MessageBrokerOption configures a MessageBroker.
type MessageBrokerOption func(*messageBrokerOptions)

// WithCancellationGracePeriod controls how long a canceled caller waits for the
// remote handler to acknowledge cancellation before returning.
func WithCancellationGracePeriod(gracePeriod time.Duration) MessageBrokerOption {
	return func(options *messageBrokerOptions) {
		if gracePeriod >= 0 {
			options.cancellationGracePeriod = gracePeriod
		}
	}
}

// handlerWrapper wraps a registered handler function with metadata
type handlerWrapper struct {
	handlerFunc   reflect.Value
	requestType   reflect.Type
	responseType  reflect.Type
	hasProgress   bool
	progressIndex int // parameter index for progress callback
}

type activeRequest struct {
	cancel context.CancelCauseFunc
}

type responseWaiter[T any] struct {
	ch        chan *T
	done      chan struct{}
	closeOnce sync.Once
}

func newResponseWaiter[T any](buffer int) *responseWaiter[T] {
	return &responseWaiter[T]{
		ch:   make(chan *T, buffer),
		done: make(chan struct{}),
	}
}

func (w *responseWaiter[T]) close() {
	w.closeOnce.Do(func() {
		close(w.done)
	})
}

func (w *responseWaiter[T]) send(msg *T) bool {
	select {
	case w.ch <- msg:
		return true
	case <-w.done:
		return false
	}
}

// MessageBroker handles bidirectional message routing for gRPC streams.
// It supports both client pattern (request/response correlation via RequestId)
// and server pattern (handler registration for incoming requests).
//
// TMessage is the raw message type used by the gRPC stream.
// The ops parameter provides stateless operations for manipulating messages.
//
// This broker works with both client-side (grpc.BidiStreamingClient) and
// server-side (grpc.BidiStreamingServer) streams through the unified BidiStream interface.
type MessageBroker[TMessage any] struct {
	logger          *log.Logger // Private logger for broker trace output; can be silenced independently
	stream          BidiStream[TMessage]
	envelope        MessageEnvelope[TMessage]
	name            string                                         // Name identifier for logging purposes
	responseWaiters syncmap.Map[string, *responseWaiter[TMessage]] // Pending responses by request id
	handlers        syncmap.Map[reflect.Type, *handlerWrapper]     // Registered handlers by request type
	sendMu          sync.Mutex                                     // Protects concurrent stream.Send() calls
	stateMu         sync.Mutex                                     // Protects active requests and broker closure
	active          map[string]map[*activeRequest]struct{}
	closed          bool
	closeOnce       sync.Once

	cancellationGracePeriod time.Duration

	// Ready signaling for when the broker starts receiving messages
	readyCh   chan struct{} // Closed when Run() starts, signals readiness to all waiters
	readyOnce sync.Once     // Ensures readyCh is only closed once
}

// NewMessageBroker creates a new message broker for the given stream.
// The stream parameter can be either a client stream (grpc.BidiStreamingClient)
// or a server stream (grpc.BidiStreamingServer) as both implement the BidiStream interface.
// The ops parameter provides stateless operations for message manipulation.
// The name parameter is used for logging identification.
// The logger parameter sets the broker's private logger for trace output:
// Pass [log.Default] on the server side (azd core CLI) to inherit --debug semantics.
// Pass nil for silent operation (e.g., in extension processes where AZD_EXT_DEBUG controls logging).
func NewMessageBroker[TMessage any](
	stream BidiStream[TMessage],
	ops MessageEnvelope[TMessage],
	name string,
	logger *log.Logger,
	options ...MessageBrokerOption,
) *MessageBroker[TMessage] {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	brokerOptions := messageBrokerOptions{
		cancellationGracePeriod: defaultCancellationGracePeriod,
	}
	for _, option := range options {
		if option != nil {
			option(&brokerOptions)
		}
	}

	return &MessageBroker[TMessage]{
		logger:                  logger,
		stream:                  stream,
		envelope:                ops,
		name:                    name,
		active:                  map[string]map[*activeRequest]struct{}{},
		cancellationGracePeriod: brokerOptions.cancellationGracePeriod,
		readyCh:                 make(chan struct{}),
	}
}

func (mb *MessageBroker[TMessage]) registerResponseWaiter(
	requestId string,
	waiter *responseWaiter[TMessage],
) error {
	mb.stateMu.Lock()
	defer mb.stateMu.Unlock()

	if mb.closed {
		return errMessageBrokerClosed
	}
	if _, exists := mb.responseWaiters.Load(requestId); exists {
		return fmt.Errorf("%w: %s", errRequestAlreadyPending, requestId)
	}

	mb.responseWaiters.Store(requestId, waiter)
	return nil
}

func (mb *MessageBroker[TMessage]) unregisterResponseWaiter(
	requestId string,
	waiter *responseWaiter[TMessage],
) {
	mb.stateMu.Lock()
	mb.responseWaiters.Delete(requestId)
	mb.stateMu.Unlock()
	waiter.close()
}

// On registers a handler for a specific message type.
// The handler function signature should be one of:
//   - func(ctx context.Context, req *RequestType) (*TMessage, error)
//   - func(ctx context.Context, req *RequestType, progress ProgressFunc) (*TMessage, error)
//
// The handler must return a complete envelope message. The broker will automatically
// set the RequestId and Error fields before sending the response.
func (mb *MessageBroker[TMessage]) On(handler any) error {
	handlerValue := reflect.ValueOf(handler)
	handlerType := handlerValue.Type()

	// Validate handler is a function
	if handlerType.Kind() != reflect.Func {
		return fmt.Errorf("handler must be a function, got %v", handlerType.Kind())
	}

	// Validate number of input parameters (2 or 3)
	numIn := handlerType.NumIn()
	if numIn < 2 || numIn > 3 {
		return fmt.Errorf(
			"handler must have 2 or 3 parameters (context.Context, *RequestType[, ProgressFunc]), got %d",
			numIn,
		)
	}

	// Validate first parameter is context.Context
	contextType := reflect.TypeFor[context.Context]()
	if !handlerType.In(0).Implements(contextType) {
		return fmt.Errorf("first parameter must be context.Context, got %v", handlerType.In(0))
	}

	// Extract request type (second parameter)
	requestType := handlerType.In(1)
	if requestType.Kind() != reflect.Pointer {
		return fmt.Errorf("request type must be a pointer, got %v", requestType)
	}

	// Check for optional progress parameter
	hasProgress := false
	progressIndex := -1
	if numIn == 3 {
		progressType := reflect.TypeFor[ProgressFunc]()
		if handlerType.In(2) == progressType {
			hasProgress = true
			progressIndex = 2
		} else {
			return fmt.Errorf("third parameter must be ProgressFunc, got %v", handlerType.In(2))
		}
	}

	// Validate number of output parameters (2: envelope and error)
	if handlerType.NumOut() != 2 {
		return fmt.Errorf("handler must return 2 values (*TMessage, error), got %d", handlerType.NumOut())
	}

	// Validate response type is *TMessage (pointer to envelope)
	responseType := handlerType.Out(0)
	envelopeType := reflect.TypeFor[*TMessage]()
	if responseType != envelopeType {
		return fmt.Errorf("handler must return pointer to envelope type %v, got %v", envelopeType, responseType)
	}

	// Validate error return type
	errorType := reflect.TypeFor[error]()
	if !handlerType.Out(1).Implements(errorType) {
		return fmt.Errorf("second return value must be error, got %v", handlerType.Out(1))
	}

	// Store handler wrapper
	wrapper := &handlerWrapper{
		handlerFunc:   handlerValue,
		requestType:   requestType,
		responseType:  responseType,
		hasProgress:   hasProgress,
		progressIndex: progressIndex,
	}

	mb.handlers.Store(requestType, wrapper)
	mb.logger.Printf("[%s] Registered handler for MessageType=%v", mb.name, requestType)

	return nil
}

// SendAndWait sends a message and waits for the response
func (mb *MessageBroker[TMessage]) SendAndWait(ctx context.Context, msg *TMessage) (*TMessage, error) {
	requestId := mb.envelope.GetRequestId(ctx, msg)
	if requestId == "" {
		return nil, errors.New("message must have a RequestId")
	}

	innerMsg := mb.envelope.GetInnerMessage(msg)
	msgType := reflect.TypeOf(innerMsg)
	mb.logger.Printf("[%s] [RequestId=%s] Sending request, MessageType=%v", mb.name, requestId, msgType)

	waiter := newResponseWaiter[TMessage](1)
	if err := mb.registerResponseWaiter(requestId, waiter); err != nil {
		if ctxErr := contextError(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	defer func() {
		mb.unregisterResponseWaiter(requestId, waiter)
	}()

	// Send request in goroutine to ensure we're waiting before response arrives
	errCh := make(chan error, 1)
	go func() {
		mb.sendMu.Lock()
		defer mb.sendMu.Unlock()
		errCh <- mb.stream.Send(msg)
	}()

	requestSent := false

	// Wait for send to complete, response, or context cancellation
	for {
		select {
		case <-ctx.Done():
			mb.logger.Printf("[%s] [RequestId=%s] Context cancelled, MessageType=%v", mb.name, requestId, msgType)
			return nil, mb.cancelPendingRequest(ctx, msg, requestId, msgType, waiter, errCh, requestSent)
		case <-waiter.done:
			mb.logger.Printf("[%s] [RequestId=%s] Waiter closed (broker stopped)", mb.name, requestId)
			if ctxErr := contextError(ctx); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errMessageBrokerClosed
		case err := <-errCh:
			if err != nil {
				err = wrapResourceExhausted(err, "SendAndWait")
				mb.logger.Printf(
					"[%s] [RequestId=%s] ERROR: Send failed, MessageType=%v, Error=%v",
					mb.name,
					requestId,
					msgType,
					err,
				)
				return nil, err
			}
			requestSent = true
			mb.logger.Printf("[%s] [RequestId=%s] Request sent successfully, MessageType=%v", mb.name, requestId, msgType)
		case resp := <-waiter.ch:
			respInner := mb.envelope.GetInnerMessage(resp)
			respType := reflect.TypeOf(respInner)
			mb.logger.Printf("[%s] [RequestId=%s] Received response, MessageType=%v", mb.name, requestId, respType)
			responseErr := mb.envelope.GetError(resp)
			if ctxErr := contextError(ctx); ctxErr != nil {
				if responseErr != nil && !errors.Is(responseErr, context.Canceled) &&
					!errors.Is(responseErr, context.DeadlineExceeded) {
					return nil, errors.Join(responseErr, ctxErr)
				}
				return nil, ctxErr
			}
			if responseErr != nil {
				mb.logger.Printf(
					"[%s] [RequestId=%s] Response contains error, MessageType=%v, Error=%v",
					mb.name,
					requestId,
					respType,
					responseErr,
				)
				return nil, responseErr
			}
			return resp, nil
		}
	}
}

// Send sends a message without waiting for a response.
// This is useful for fire-and-forget scenarios like subscriptions or notifications
// where no response is expected or needed.
// Returns an error only if the send operation itself fails.
func (mb *MessageBroker[TMessage]) Send(ctx context.Context, msg *TMessage) error {
	innerMsg := mb.envelope.GetInnerMessage(msg)
	msgType := reflect.TypeOf(innerMsg)
	requestId := mb.envelope.GetRequestId(ctx, msg)

	mb.logger.Printf("[%s] [RequestId=%s] Sending fire-and-forget message, MessageType=%v", mb.name, requestId, msgType)

	// Protect concurrent Send() calls with mutex
	mb.sendMu.Lock()
	defer mb.sendMu.Unlock()

	if err := mb.stream.Send(msg); err != nil {
		err = wrapResourceExhausted(err, "Send")
		mb.logger.Printf(
			"[%s] [RequestId=%s] ERROR: Failed to send fire-and-forget message, MessageType=%v, Error=%v",
			mb.name,
			requestId,
			msgType,
			err,
		)
		return err
	}

	mb.logger.Printf(
		"[%s] [RequestId=%s] Fire-and-forget message sent successfully, MessageType=%v",
		mb.name,
		requestId,
		msgType,
	)
	return nil
}

// SendAndWaitWithProgress sends a message and waits for the response, handling progress updates
func (mb *MessageBroker[TMessage]) SendAndWaitWithProgress(
	ctx context.Context,
	msg *TMessage,
	onProgress func(string),
) (*TMessage, error) {
	requestId := mb.envelope.GetRequestId(ctx, msg)
	if requestId == "" {
		return nil, errors.New("message must have a RequestId")
	}

	innerMsg := mb.envelope.GetInnerMessage(msg)
	msgType := reflect.TypeOf(innerMsg)

	// Use a larger buffer to handle multiple progress messages without blocking the dispatcher
	waiter := newResponseWaiter[TMessage](50)
	mb.logger.Printf("[%s] [RequestId=%s] Registering waiter, MessageType=%v", mb.name, requestId, msgType)
	if err := mb.registerResponseWaiter(requestId, waiter); err != nil {
		if ctxErr := contextError(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	defer func() {
		mb.logger.Printf("[%s] [RequestId=%s] Cleaning up waiter", mb.name, requestId)
		mb.unregisterResponseWaiter(requestId, waiter)
	}()

	// Send request in goroutine to ensure we're waiting before response arrives
	mb.logger.Printf("[%s] [RequestId=%s] Sending request, MessageType=%v", mb.name, requestId, msgType)
	errCh := make(chan error, 1)
	go func() {
		mb.sendMu.Lock()
		defer mb.sendMu.Unlock()
		errCh <- mb.stream.Send(msg)
	}()

	requestSent := false

	// Wait for responses, send completion, or context cancellation
	for {
		select {
		case <-ctx.Done():
			mb.logger.Printf(
				"[%s] [RequestId=%s] Context cancelled, MessageType=%v, Error=%v",
				mb.name,
				requestId,
				msgType,
				contextError(ctx),
			)
			return nil, mb.cancelPendingRequest(ctx, msg, requestId, msgType, waiter, errCh, requestSent)
		case <-waiter.done:
			mb.logger.Printf("[%s] [RequestId=%s] Waiter closed (dispatcher stopped)", mb.name, requestId)
			if ctxErr := contextError(ctx); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, errMessageBrokerClosed
		case err := <-errCh:
			if err != nil {
				err = wrapResourceExhausted(err, "SendAndWaitWithProgress")
				mb.logger.Printf(
					"[%s] [RequestId=%s] ERROR: Failed to send request, MessageType=%v, Error=%v",
					mb.name,
					requestId,
					msgType,
					err,
				)
				return nil, err
			}
			requestSent = true
			mb.logger.Printf(
				"[%s] [RequestId=%s] Request sent successfully, MessageType=%v, waiting for response",
				mb.name,
				requestId,
				msgType,
			)
		case resp := <-waiter.ch:
			respInner := mb.envelope.GetInnerMessage(resp)
			respType := reflect.TypeOf(respInner)
			mb.logger.Printf("[%s] [RequestId=%s] Received on channel, MessageType=%v", mb.name, requestId, respType)

			// Check if this is a progress message
			if mb.envelope.IsProgressMessage(resp) {
				mb.logger.Printf("[%s] [RequestId=%s] Progress message, MessageType=%v", mb.name, requestId, respType)
				if onProgress != nil {
					progressText := mb.envelope.GetProgressMessage(resp)
					if progressText != "" {
						onProgress(progressText)
					}
				}
				// Continue waiting for more messages
				continue
			}

			// Any non-progress message with matching RequestId is our final response
			mb.logger.Printf("[%s] [RequestId=%s] Received final response, MessageType=%v", mb.name, requestId, respType)
			responseErr := mb.envelope.GetError(resp)
			if ctxErr := contextError(ctx); ctxErr != nil {
				if responseErr != nil && !errors.Is(responseErr, context.Canceled) &&
					!errors.Is(responseErr, context.DeadlineExceeded) {
					return nil, errors.Join(responseErr, ctxErr)
				}
				return nil, ctxErr
			}
			if responseErr != nil {
				mb.logger.Printf(
					"[%s] [RequestId=%s] Response contains error, MessageType=%v, Error=%v",
					mb.name,
					requestId,
					respType,
					responseErr,
				)
				return nil, responseErr
			}
			return resp, nil
		}
	}
}

func (mb *MessageBroker[TMessage]) cancelPendingRequest(
	ctx context.Context,
	request *TMessage,
	requestId string,
	msgType reflect.Type,
	waiter *responseWaiter[TMessage],
	requestSendErrCh <-chan error,
	requestSent bool,
) error {
	cause := contextError(ctx)
	if cause == nil {
		cause = context.Canceled
	}

	cancellationMessage := mb.createCancellationMessage(ctx, request, cause)
	if cancellationMessage == nil {
		return cause
	}

	if !requestSent {
		select {
		case err := <-requestSendErrCh:
			if err != nil {
				return errors.Join(cause, wrapResourceExhausted(err, "Send canceled request"))
			}
		case <-time.After(mb.cancellationGracePeriod):
			go func() {
				if err := <-requestSendErrCh; err == nil {
					_ = mb.sendCancellationMessage(requestId, msgType, cancellationMessage)
				}
			}()
			return cause
		}
	}

	cancelSendErrCh := make(chan error, 1)
	go func() {
		cancelSendErrCh <- mb.sendCancellationMessage(requestId, msgType, cancellationMessage)
	}()

	timer := time.NewTimer(mb.cancellationGracePeriod)
	defer timer.Stop()

	for {
		select {
		case <-waiter.done:
			return cause
		case response := <-waiter.ch:
			if mb.envelope.IsProgressMessage(response) {
				continue
			}
			if responseErr := mb.envelope.GetError(response); responseErr != nil {
				if errors.Is(responseErr, context.Canceled) ||
					errors.Is(responseErr, context.DeadlineExceeded) {
					return cause
				}
				return errors.Join(responseErr, cause)
			}
			return cause
		case sendErr := <-cancelSendErrCh:
			if sendErr != nil {
				return errors.Join(cause, sendErr)
			}
			cancelSendErrCh = nil
		case <-timer.C:
			return cause
		}
	}
}

func (mb *MessageBroker[TMessage]) sendCancellationMessage(
	requestId string,
	msgType reflect.Type,
	msg *TMessage,
) error {
	mb.logger.Printf(
		"[%s] [RequestId=%s] Sending cancellation, MessageType=%v",
		mb.name,
		requestId,
		msgType,
	)

	mb.sendMu.Lock()
	defer mb.sendMu.Unlock()

	if err := mb.stream.Send(msg); err != nil {
		err = wrapResourceExhausted(err, "Send cancellation")
		mb.logger.Printf(
			"[%s] [RequestId=%s] ERROR: Cancellation send failed, MessageType=%v, Error=%v",
			mb.name,
			requestId,
			msgType,
			err,
		)
		return err
	}

	return nil
}

func (mb *MessageBroker[TMessage]) createCancellationMessage(
	ctx context.Context,
	request *TMessage,
	err error,
) *TMessage {
	if envelope, ok := mb.envelope.(CancellationMessageEnvelope[TMessage]); ok {
		return envelope.CreateCancellationMessage(ctx, request, err)
	}

	requestId := mb.envelope.GetRequestId(ctx, request)
	if requestId == "" {
		return nil
	}

	msg := new(TMessage)
	mb.envelope.SetRequestId(ctx, msg, requestId)
	mb.envelope.SetError(msg, err)
	if mb.envelope.GetRequestId(ctx, msg) == "" || mb.envelope.GetError(msg) == nil {
		return nil
	}

	return msg
}

func (mb *MessageBroker[TMessage]) isCancellationMessage(ctx context.Context, msg *TMessage) bool {
	if envelope, ok := mb.envelope.(CancellationMessageEnvelope[TMessage]); ok {
		return envelope.IsCancellationMessage(ctx, msg)
	}

	if mb.envelope.GetRequestId(ctx, msg) == "" || mb.envelope.GetInnerMessage(msg) != nil {
		return false
	}

	err := mb.envelope.GetError(msg)
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (mb *MessageBroker[TMessage]) cancellationError(msg *TMessage) error {
	if envelope, ok := mb.envelope.(CancellationMessageEnvelope[TMessage]); ok {
		return envelope.GetCancellationError(msg)
	}

	return mb.envelope.GetError(msg)
}

// Ready blocks until the message broker starts receiving messages or the context is cancelled.
// Multiple goroutines can call Ready() simultaneously - they will all be unblocked when Run() starts.
// Once the broker has started, subsequent calls to Ready() return immediately.
// Returns nil when ready, or context error if the context is cancelled before the broker becomes ready.
func (mb *MessageBroker[TMessage]) Ready(ctx context.Context) error {
	select {
	case <-mb.readyCh:
		// Broker is ready (channel closed by Run method)
		return nil
	case <-ctx.Done():
		// Context cancelled before broker became ready
		return contextError(ctx)
	}
}

// Run begins receiving and dispatching messages.
// This method blocks until the context is cancelled, the stream encounters an error,
// or the stream is closed by the remote peer.
// Returns nil on graceful shutdown (context cancelled or EOF), or the error that terminated the stream.
func (mb *MessageBroker[TMessage]) Run(ctx context.Context) error {
	// Signal that the broker is ready to receive messages
	mb.readyOnce.Do(func() {
		close(mb.readyCh)
	})

	defer func() {
		mb.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			mb.logger.Printf("[%s] Dispatcher stopped due to context cancellation", mb.name)
			return contextError(ctx)
		default:
			resp, err := mb.stream.Recv()
			if err != nil {
				err = wrapResourceExhausted(err, "Recv")

				// Any error from stream.Recv() is terminal for this stream.
				// Check for graceful closure conditions:
				// 1. Direct io.EOF
				// 2. gRPC Unavailable with EOF in the message (wrapped EOF from stream close)
				// 3. gRPC Canceled (context cancellation propagated through gRPC)
				if errors.Is(err, io.EOF) {
					return nil
				}

				// Check for gRPC status codes that indicate graceful closure
				if st, ok := status.FromError(err); ok {
					if st.Code() == codes.Unavailable && strings.Contains(st.Message(), "EOF") {
						return nil
					}
					if st.Code() == codes.Canceled {
						return contextError(ctx)
					}
				}

				mb.logger.Printf("[%s] ERROR: Stream receive failed: %v", mb.name, err)
				return fmt.Errorf("stream receive failed: %w", err)
			}

			// Route the message synchronously so a request-specific handler context
			// is registered before a following cancellation message is received.
			mb.processMessage(ctx, resp)
		}
	}
}

// processMessage handles routing and processing of a received message.
// Messages are either routed to awaiting channels (client pattern) or dispatched to handlers (server pattern).
func (mb *MessageBroker[TMessage]) processMessage(ctx context.Context, resp *TMessage) {
	innerMsg := mb.envelope.GetInnerMessage(resp)
	msgType := reflect.TypeOf(innerMsg)
	requestId := mb.envelope.GetRequestId(ctx, resp)

	// Check if this is a progress message - always route to channel, never to handler
	if mb.envelope.IsProgressMessage(resp) {
		mb.logger.Printf("[%s] Received progress message: RequestId=%s, MessageType=%v", mb.name, requestId, msgType)
		if waiter, ok := mb.responseWaiters.Load(requestId); ok {
			mb.logger.Printf(
				"[%s] Dispatching progress message to channel for RequestId=%s, MessageType=%v",
				mb.name,
				requestId,
				msgType,
			)
			waiter.send(resp)
		} else {
			mb.logger.Printf(
				"[%s] WARNING: No channel found for progress message RequestId=%s, MessageType=%v",
				mb.name,
				requestId,
				msgType,
			)
		}
		return
	}

	mb.logger.Printf("[%s] Dispatcher received message: RequestId=%s, MessageType=%v", mb.name, requestId, msgType)

	// Try to route to channel first (client pattern - awaiting response)
	if requestId != "" {
		if waiter, ok := mb.responseWaiters.Load(requestId); ok {
			// Warn when channel buffer is actually nearly full
			if cap(waiter.ch) > 1 && len(waiter.ch) >= cap(waiter.ch)-1 {
				mb.logger.Printf(
					"[%s] WARNING: Channel buffer nearly full for RequestId=%s (len=%d, cap=%d)",
					mb.name,
					requestId,
					len(waiter.ch),
					cap(waiter.ch),
				)
			}

			mb.logger.Printf("[%s] Dispatching message to channel for RequestId=%s, MessageType=%v",
				mb.name, requestId, msgType)
			if waiter.send(resp) {
				mb.logger.Printf("[%s] Message dispatched successfully to RequestId=%s, MessageType=%v",
					mb.name, requestId, msgType)
			}
			return
		}
	}

	if mb.isCancellationMessage(ctx, resp) {
		if mb.cancelActiveRequest(requestId, mb.cancellationError(resp)) {
			mb.logger.Printf("[%s] Cancelled active handler for RequestId=%s", mb.name, requestId)
		} else {
			mb.logger.Printf("[%s] No active handler found for cancellation RequestId=%s", mb.name, requestId)
		}
		return
	}

	// No channel found, try to route to handler (server pattern - incoming request)
	mb.startHandlerRequest(ctx, resp, requestId, msgType)
}

func (mb *MessageBroker[TMessage]) startHandlerRequest(
	ctx context.Context,
	envelope *TMessage,
	requestId string,
	msgType reflect.Type,
) {
	mb.stateMu.Lock()
	if mb.closed {
		mb.stateMu.Unlock()
		return
	}

	handlerCtx := ctx
	var request *activeRequest
	preserveHandlerContext := false
	if persistentEnvelope, ok := mb.envelope.(PersistentHandlerContextEnvelope[TMessage]); ok {
		preserveHandlerContext = persistentEnvelope.PreserveHandlerContext(ctx, envelope)
	}
	if requestId != "" && !preserveHandlerContext {
		var cancel context.CancelCauseFunc
		handlerCtx, cancel = context.WithCancelCause(ctx)
		request = &activeRequest{cancel: cancel}

		requests := mb.active[requestId]
		if requests == nil {
			requests = map[*activeRequest]struct{}{}
			mb.active[requestId] = requests
		}
		requests[request] = struct{}{}
	}
	mb.stateMu.Unlock()

	go func() {
		if request != nil {
			defer mb.finishActiveRequest(requestId, request)
		}
		mb.processHandlerRequest(handlerCtx, envelope, requestId, msgType)
	}()
}

func (mb *MessageBroker[TMessage]) cancelActiveRequest(requestId string, cause error) bool {
	if requestId == "" {
		return false
	}
	if cause == nil {
		cause = context.Canceled
	}

	mb.stateMu.Lock()
	requests := make([]*activeRequest, 0, len(mb.active[requestId]))
	for request := range mb.active[requestId] {
		requests = append(requests, request)
	}
	mb.stateMu.Unlock()
	if len(requests) == 0 {
		return false
	}

	for _, request := range requests {
		request.cancel(cause)
	}
	return true
}

func (mb *MessageBroker[TMessage]) finishActiveRequest(requestId string, request *activeRequest) {
	mb.stateMu.Lock()
	if requests := mb.active[requestId]; requests != nil {
		delete(requests, request)
	}
	if len(mb.active[requestId]) == 0 {
		delete(mb.active, requestId)
	}
	mb.stateMu.Unlock()
	request.cancel(nil)
}

// processHandlerRequest extracts the inner message, finds the appropriate handler,
// invokes it, and sends the response back on the stream.
func (mb *MessageBroker[TMessage]) processHandlerRequest(
	ctx context.Context,
	envelope *TMessage,
	requestId string,
	msgType reflect.Type,
) {
	innerMsg := mb.envelope.GetInnerMessage(envelope)
	if innerMsg == nil {
		mb.logger.Printf("[%s] WARNING: No inner message found for RequestId=%s, MessageType=%v",
			mb.name, requestId, msgType)
		return
	}

	handlerVal, ok := mb.handlers.Load(msgType)
	if !ok {
		mb.logger.Printf(
			"[%s] WARNING: No handler registered for RequestId=%s, MessageType=%v - message dropped",
			mb.name,
			requestId,
			msgType,
		)
		return
	}

	wrapper := handlerVal
	mb.logger.Printf(
		"[%s] Dispatching to handler for RequestId=%s, MessageType=%v",
		mb.name,
		requestId,
		msgType,
	)

	// Invoke handler
	responseEnvelope := mb.invokeHandler(ctx, wrapper, envelope, innerMsg)
	if responseEnvelope != nil {
		// Protect concurrent Send() calls with mutex
		mb.sendMu.Lock()
		defer mb.sendMu.Unlock()

		if err := mb.stream.Send(responseEnvelope); err != nil {
			err = wrapResourceExhausted(err, "Send handler response")
			mb.logger.Printf(
				"[%s] ERROR: Failed to send handler response: RequestId=%s, MessageType=%v, Error=%v",
				mb.name,
				requestId,
				msgType,
				err,
			)
		} else {
			mb.logger.Printf(
				"[%s] Handler response sent successfully for RequestId=%s, MessageType=%v",
				mb.name,
				requestId,
				msgType,
			)
		}
	}
}

// invokeHandler calls the registered handler and sends the response envelope
func (mb *MessageBroker[TMessage]) invokeHandler(
	ctx context.Context,
	wrapper *handlerWrapper,
	envelope *TMessage,
	innerMsg any,
) *TMessage {
	requestId := mb.envelope.GetRequestId(ctx, envelope)

	// Prepare arguments for handler invocation
	args := []reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(innerMsg),
	}

	// Add progress callback if handler expects it
	if wrapper.hasProgress {
		progressFunc := mb.createProgressFunc(ctx, requestId)
		args = append(args, reflect.ValueOf(progressFunc))
	}

	// results[0] = envelope (may be nil), results[1] = error (may be nil)
	var responseEnvelope *TMessage
	var handlerErr error

	// Invoke handler via reflection with panic recovery
	func() {
		defer func() {
			if r := recover(); r != nil {
				mb.logger.Printf(
					"[%s] PANIC: Handler panicked for RequestId=%s, MessageType=%v, panic=%v",
					mb.name,
					requestId,
					wrapper.requestType,
					r,
				)
				// Convert panic to error so client gets a proper error response
				handlerErr = fmt.Errorf("handler panicked: %v", r)
			}
		}()

		results := wrapper.handlerFunc.Call(args)

		if len(results) > 0 && !results[0].IsNil() {
			responseEnvelope = results[0].Interface().(*TMessage)
		}
		if len(results) > 1 && !results[1].IsNil() {
			handlerErr = results[1].Interface().(error)
		}
	}()

	if ctxErr := contextError(ctx); handlerErr == nil ||
		(ctxErr != nil && errors.Is(handlerErr, ctx.Err())) {
		handlerErr = ctxErr
	}

	// If handler returned nil envelope and no error, suppress the response entirely.
	// This allows handlers to return (nil, nil) to indicate no response should be sent,
	// which is used by intermediate chunk handlers that don't need acknowledgment.
	if responseEnvelope == nil && handlerErr == nil {
		return nil
	}

	// If handler returned nil envelope but has an error, create one to carry the error
	if responseEnvelope == nil {
		responseEnvelope = new(TMessage)
	}

	// Broker automatically sets RequestId and Error on the envelope
	mb.envelope.SetRequestId(ctx, responseEnvelope, requestId)

	if handlerErr != nil {
		// Auto-set error on envelope
		mb.logger.Printf("[%s] Handler returned error for RequestId=%s: %v", mb.name, requestId, handlerErr)
		mb.envelope.SetError(responseEnvelope, handlerErr)
	}

	return responseEnvelope
}

func contextError(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ctx.Err()
}

// createProgressFunc creates a progress callback function for a given request ID
func (mb *MessageBroker[TMessage]) createProgressFunc(ctx context.Context, requestId string) ProgressFunc {
	return func(message string) {
		mb.logger.Printf("[%s] Sending progress for RequestId=%s: %s", mb.name, requestId, message)

		// Create progress envelope using the envelope's factory method
		progressEnvelope := mb.envelope.CreateProgressMessage(requestId, message)

		// Send the progress message on the stream (protected by mutex for concurrent access)
		mb.sendMu.Lock()
		defer mb.sendMu.Unlock()

		if err := mb.stream.Send(progressEnvelope); err != nil {
			err = wrapResourceExhausted(err, "Send progress")
			mb.logger.Printf("[%s] ERROR: Failed to send progress message for RequestId=%s: %v", mb.name, requestId, err)
		}
	}
}

// Close gracefully shuts down the broker (optional, for cleanup)
func (mb *MessageBroker[TMessage]) Close() {
	mb.closeOnce.Do(func() {
		mb.stateMu.Lock()
		mb.closed = true

		var activeRequests []*activeRequest
		for requestId, requests := range mb.active {
			for request := range requests {
				activeRequests = append(activeRequests, request)
			}
			delete(mb.active, requestId)
		}

		var responseWaiters []*responseWaiter[TMessage]
		mb.responseWaiters.Range(func(key string, waiter *responseWaiter[TMessage]) bool {
			responseWaiters = append(responseWaiters, waiter)
			mb.responseWaiters.Delete(key)
			return true
		})
		mb.stateMu.Unlock()

		for _, request := range activeRequests {
			request.cancel(context.Canceled)
		}
		for _, waiter := range responseWaiters {
			waiter.close()
		}
	})
}
