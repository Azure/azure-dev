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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// handlerWrapper wraps a registered handler function with metadata
type handlerWrapper struct {
	handlerFunc   reflect.Value
	requestType   reflect.Type
	responseType  reflect.Type
	hasProgress   bool
	progressIndex int // parameter index for progress callback
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
	stream        BidiStream[TMessage]
	envelope      MessageEnvelope[TMessage]
	responseChans sync.Map   // Used for storing response channels by request id
	handlers      sync.Map   // Used for storing message handlers by request type
	sendMu        sync.Mutex // Protects concurrent stream.Send() calls
}

// NewMessageBroker creates a new message broker for the given stream.
// The stream parameter can be either a client stream (grpc.BidiStreamingClient)
// or a server stream (grpc.BidiStreamingServer) as both implement the BidiStream interface.
// The ops parameter provides stateless operations for message manipulation.
func NewMessageBroker[TMessage any](
	stream BidiStream[TMessage],
	ops MessageEnvelope[TMessage],
) *MessageBroker[TMessage] {
	return &MessageBroker[TMessage]{
		stream:   stream,
		envelope: ops,
	}
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
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	if !handlerType.In(0).Implements(contextType) {
		return fmt.Errorf("first parameter must be context.Context, got %v", handlerType.In(0))
	}

	// Extract request type (second parameter)
	requestType := handlerType.In(1)
	if requestType.Kind() != reflect.Ptr {
		return fmt.Errorf("request type must be a pointer, got %v", requestType)
	}

	// Check for optional progress parameter
	hasProgress := false
	progressIndex := -1
	if numIn == 3 {
		progressType := reflect.TypeOf((*ProgressFunc)(nil)).Elem()
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
	var envelopeZero TMessage
	envelopeType := reflect.TypeOf(&envelopeZero)
	if responseType != envelopeType {
		return fmt.Errorf("handler must return pointer to envelope type %v, got %v", envelopeType, responseType)
	}

	// Validate error return type
	errorType := reflect.TypeOf((*error)(nil)).Elem()
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
	log.Printf("[MessageBroker] Registered handler for MessageType=%v", requestType)

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
	log.Printf("[MessageBroker] [RequestId=%s] Sending request, MessageType=%v", requestId, msgType)

	ch := make(chan *TMessage, 1)
	mb.responseChans.Store(requestId, ch)
	defer mb.responseChans.Delete(requestId)

	// Send request in goroutine to ensure we're waiting before response arrives
	errCh := make(chan error, 1)
	go func() {
		errCh <- mb.stream.Send(msg)
	}()

	// Wait for send to complete, response, or context cancellation
	for {
		select {
		case <-ctx.Done():
			log.Printf("[MessageBroker] [RequestId=%s] Context cancelled, MessageType=%v", requestId, msgType)
			return nil, ctx.Err()
		case err := <-errCh:
			if err != nil {
				log.Printf(
					"[MessageBroker] [RequestId=%s] ERROR: Send failed, MessageType=%v, Error=%v",
					requestId,
					msgType,
					err,
				)
				return nil, err
			}
			log.Printf("[MessageBroker] [RequestId=%s] Request sent successfully, MessageType=%v", requestId, msgType)
		case resp := <-ch:
			respInner := mb.envelope.GetInnerMessage(resp)
			respType := reflect.TypeOf(respInner)
			log.Printf("[MessageBroker] [RequestId=%s] Received response, MessageType=%v", requestId, respType)
			if err := mb.envelope.GetError(resp); err != nil {
				log.Printf(
					"[MessageBroker] [RequestId=%s] Response contains error, MessageType=%v, Error=%v",
					requestId,
					respType,
					err,
				)
				return nil, err
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

	log.Printf("[MessageBroker] [RequestId=%s] Sending fire-and-forget message, MessageType=%v", requestId, msgType)

	// Protect concurrent Send() calls with mutex
	mb.sendMu.Lock()
	defer mb.sendMu.Unlock()

	if err := mb.stream.Send(msg); err != nil {
		log.Printf(
			"[MessageBroker] [RequestId=%s] ERROR: Failed to send fire-and-forget message, MessageType=%v, Error=%v",
			requestId,
			msgType,
			err,
		)
		return err
	}

	log.Printf(
		"[MessageBroker] [RequestId=%s] Fire-and-forget message sent successfully, MessageType=%v",
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
	ch := make(chan *TMessage, 50)
	log.Printf("[MessageBroker] [RequestId=%s] Registering channel, MessageType=%v", requestId, msgType)
	mb.responseChans.Store(requestId, ch)
	defer func() {
		log.Printf("[MessageBroker] [RequestId=%s] Cleaning up channel", requestId)
		mb.responseChans.Delete(requestId)
	}()

	// Send request in goroutine to ensure we're waiting before response arrives
	log.Printf("[MessageBroker] [RequestId=%s] Sending request, MessageType=%v", requestId, msgType)
	errCh := make(chan error, 1)
	go func() {
		errCh <- mb.stream.Send(msg)
	}()

	// Wait for responses, send completion, or context cancellation
	for {
		select {
		case <-ctx.Done():
			log.Printf(
				"[MessageBroker] [RequestId=%s] Context cancelled, MessageType=%v, Error=%v",
				requestId,
				msgType,
				ctx.Err(),
			)
			return nil, ctx.Err()
		case err := <-errCh:
			if err != nil {
				log.Printf(
					"[MessageBroker] [RequestId=%s] ERROR: Failed to send request, MessageType=%v, Error=%v",
					requestId,
					msgType,
					err,
				)
				return nil, err
			}
			log.Printf(
				"[MessageBroker] [RequestId=%s] Request sent successfully, MessageType=%v, waiting for response",
				requestId,
				msgType,
			)
		case resp, ok := <-ch:
			if !ok {
				log.Printf("[MessageBroker] [RequestId=%s] Channel closed (dispatcher likely stopped)", requestId)
				return nil, errors.New("channel closed by dispatcher")
			}

			respInner := mb.envelope.GetInnerMessage(resp)
			respType := reflect.TypeOf(respInner)
			log.Printf("[MessageBroker] [RequestId=%s] Received on channel, MessageType=%v", requestId, respType)

			// Check if this is a progress message
			if mb.envelope.IsProgressMessage(resp) {
				log.Printf("[MessageBroker] [RequestId=%s] Progress message, MessageType=%v", requestId, respType)
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
			log.Printf("[MessageBroker] [RequestId=%s] Received final response, MessageType=%v", requestId, respType)
			if err := mb.envelope.GetError(resp); err != nil {
				log.Printf(
					"[MessageBroker] [RequestId=%s] Response contains error, MessageType=%v, Error=%v",
					requestId,
					respType,
					err,
				)
				return nil, err
			}
			return resp, nil
		}
	}
}

// Run begins receiving and dispatching messages.
// This method blocks until the context is cancelled, the stream encounters an error,
// or the stream is closed by the remote peer.
// Returns nil on graceful shutdown (context cancelled or EOF), or the error that terminated the stream.
func (mb *MessageBroker[TMessage]) Run(ctx context.Context) error {
	log.Printf("[MessageBroker] Dispatcher starting")
	defer func() {
		log.Printf("[MessageBroker] Dispatcher stopped, cleaning up channels")
		mb.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[MessageBroker] Dispatcher stopped due to context cancellation")
			return ctx.Err()
		default:
			resp, err := mb.stream.Recv()
			if err != nil {
				// Any error from stream.Recv() is terminal for this stream.
				// Check for graceful closure conditions:
				// 1. Direct io.EOF
				// 2. gRPC Unavailable with EOF in the message (wrapped EOF from stream close)
				// 3. gRPC Canceled (context cancellation propagated through gRPC)
				if errors.Is(err, io.EOF) {
					log.Printf("[MessageBroker] Stream closed gracefully (EOF)")
					return nil
				}

				// Check for gRPC status codes that indicate graceful closure
				if st, ok := status.FromError(err); ok {
					if st.Code() == codes.Unavailable && strings.Contains(st.Message(), "EOF") {
						log.Printf("[MessageBroker] Stream closed gracefully (gRPC Unavailable with EOF)")
						return nil
					}
					if st.Code() == codes.Canceled {
						log.Printf("[MessageBroker] Stream closed due to cancellation")
						return ctx.Err()
					}
				}

				log.Printf("[MessageBroker] ERROR: Stream receive failed: %v", err)
				return fmt.Errorf("stream receive failed: %w", err)
			}

			// Process the received message asynchronously
			// This allows the dispatcher to continue receiving while handlers execute
			go mb.processMessage(ctx, resp)
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
		log.Printf("[MessageBroker] Received progress message: RequestId=%s, MessageType=%v", requestId, msgType)
		if ch, ok := mb.responseChans.Load(requestId); ok {
			channelTyped := ch.(chan *TMessage)
			log.Printf(
				"[MessageBroker] Dispatching progress message to channel for RequestId=%s, MessageType=%v",
				requestId,
				msgType,
			)
			channelTyped <- resp
		} else {
			log.Printf(
				"[MessageBroker] WARNING: No channel found for progress message RequestId=%s, MessageType=%v",
				requestId,
				msgType,
			)
		}
		return
	}

	log.Printf("[MessageBroker] Dispatcher received message: RequestId=%s, MessageType=%v", requestId, msgType)

	// Try to route to channel first (client pattern - awaiting response)
	if requestId != "" {
		if ch, ok := mb.responseChans.Load(requestId); ok {
			channelTyped := ch.(chan *TMessage)

			// Check if channel is full
			if len(channelTyped) >= cap(channelTyped)-1 {
				log.Printf(
					"[MessageBroker] WARNING: Channel buffer nearly full for RequestId=%s (len=%d, cap=%d)",
					requestId,
					len(channelTyped),
					cap(channelTyped),
				)
			}

			log.Printf("[MessageBroker] Dispatching message to channel for RequestId=%s, MessageType=%v", requestId, msgType)
			channelTyped <- resp
			log.Printf("[MessageBroker] Message dispatched successfully to RequestId=%s, MessageType=%v", requestId, msgType)
			return
		}
	}

	// No channel found, try to route to handler (server pattern - incoming request)
	mb.processHandlerRequest(ctx, resp, requestId, msgType)
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
		log.Printf("[MessageBroker] WARNING: No inner message found for RequestId=%s, MessageType=%v", requestId, msgType)
		return
	}

	handlerVal, ok := mb.handlers.Load(msgType)
	if !ok {
		log.Printf(
			"[MessageBroker] WARNING: No handler registered for RequestId=%s, MessageType=%v - message dropped",
			requestId,
			msgType,
		)
		return
	}

	wrapper := handlerVal.(*handlerWrapper)
	log.Printf(
		"[MessageBroker] Dispatching to handler for RequestId=%s, MessageType=%v",
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
			log.Printf(
				"[MessageBroker] ERROR: Failed to send handler response: RequestId=%s, MessageType=%v, Error=%v",
				requestId,
				msgType,
				err,
			)
		} else {
			log.Printf(
				"[MessageBroker] Handler response sent successfully for RequestId=%s, MessageType=%v",
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

	// Invoke handler via reflection
	results := wrapper.handlerFunc.Call(args)

	// results[0] = envelope (may be nil), results[1] = error (may be nil)
	var responseEnvelope *TMessage
	var handlerErr error

	if len(results) > 0 && !results[0].IsNil() {
		responseEnvelope = results[0].Interface().(*TMessage)
	}
	if len(results) > 1 && !results[1].IsNil() {
		handlerErr = results[1].Interface().(error)
	}

	// If handler returned nil envelope, create a new one
	if responseEnvelope == nil {
		responseEnvelope = new(TMessage)
	}

	// Broker automatically sets RequestId and Error on the envelope
	mb.envelope.SetRequestId(ctx, responseEnvelope, requestId)

	if handlerErr != nil {
		// Auto-set error on envelope
		log.Printf("[MessageBroker] Handler returned error for RequestId=%s: %v", requestId, handlerErr)
		mb.envelope.SetError(responseEnvelope, handlerErr)
	}

	return responseEnvelope
}

// createProgressFunc creates a progress callback function for a given request ID
func (mb *MessageBroker[TMessage]) createProgressFunc(ctx context.Context, requestId string) ProgressFunc {
	return func(message string) {
		log.Printf("[MessageBroker] Sending progress for RequestId=%s: %s", requestId, message)

		// Create progress envelope using the envelope's factory method
		progressEnvelope := mb.envelope.CreateProgressMessage(requestId, message)

		// Send the progress message on the stream (protected by mutex for concurrent access)
		mb.sendMu.Lock()
		defer mb.sendMu.Unlock()

		if err := mb.stream.Send(progressEnvelope); err != nil {
			log.Printf("[MessageBroker] ERROR: Failed to send progress message for RequestId=%s: %v", requestId, err)
		}
	}
}

// Close gracefully shuts down the broker (optional, for cleanup)
func (mb *MessageBroker[TMessage]) Close() {
	// Close all pending channels
	mb.responseChans.Range(func(key, value any) bool {
		ch := value.(chan *TMessage)
		close(ch)
		mb.responseChans.Delete(key)
		return true
	})
}
