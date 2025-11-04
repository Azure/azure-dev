// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcbroker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"

	"google.golang.org/grpc"
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
type MessageBroker[TMessage any] struct {
	stream        grpc.BidiStreamingServer[TMessage, TMessage]
	ops           MessageEnvelope[TMessage]
	responseChans sync.Map // map[string]chan TMessage - for client pattern
	handlers      sync.Map // map[reflect.Type]*handlerWrapper - for server pattern
}

// NewMessageBroker creates a new message broker for the given stream.
// The ops parameter provides stateless operations for message manipulation.
func NewMessageBroker[TMessage any](
	stream grpc.BidiStreamingServer[TMessage, TMessage],
	ops MessageEnvelope[TMessage],
) *MessageBroker[TMessage] {
	return &MessageBroker[TMessage]{
		stream: stream,
		ops:    ops,
	}
}

// On registers a handler for a specific message type.
// The handler function signature should be one of:
//   - func(ctx context.Context, req *RequestType) (TMessage, error)
//   - func(ctx context.Context, req *RequestType, progress ProgressFunc) (TMessage, error)
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
		return fmt.Errorf("handler must return 2 values (TMessage, error), got %d", handlerType.NumOut())
	}

	// Validate response type is TMessage
	responseType := handlerType.Out(0)
	var envelopeZero TMessage
	envelopeType := reflect.TypeOf(&envelopeZero).Elem()
	if responseType != envelopeType {
		return fmt.Errorf("handler must return envelope type %v, got %v", envelopeType, responseType)
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
	log.Printf("[MessageBroker] Registered handler for message type: %v", requestType)

	return nil
}

// Send sends a message and waits for the response
func (mb *MessageBroker[TMessage]) Send(ctx context.Context, msg *TMessage) (*TMessage, error) {
	requestId := mb.ops.GetRequestId(ctx, msg)
	if requestId == "" {
		return nil, errors.New("message must have a RequestId")
	}

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
			return nil, ctx.Err()
		case err := <-errCh:
			if err != nil {
				return nil, err
			}
		case resp := <-ch:
			if err := mb.ops.GetError(resp); err != nil {
				return nil, err
			}
			return resp, nil
		}
	}
}

// SendWithProgress sends a message and waits for the response, handling progress updates
func (mb *MessageBroker[TMessage]) SendWithProgress(
	ctx context.Context,
	msg *TMessage,
	onProgress func(string),
) (*TMessage, error) {
	requestId := mb.ops.GetRequestId(ctx, msg)
	if requestId == "" {
		return nil, errors.New("message must have a RequestId")
	}

	// Use a larger buffer to handle multiple progress messages without blocking the dispatcher
	ch := make(chan *TMessage, 50)
	log.Printf("[MessageBroker] [RequestId=%s] Registering channel, Type=%T", requestId, msg)
	mb.responseChans.Store(requestId, ch)
	defer func() {
		log.Printf("[MessageBroker] [RequestId=%s] Cleaning up channel", requestId)
		mb.responseChans.Delete(requestId)
	}()

	// Send request in goroutine to ensure we're waiting before response arrives
	log.Printf("[MessageBroker] [RequestId=%s] Sending request, Type=%T", requestId, msg)
	errCh := make(chan error, 1)
	go func() {
		errCh <- mb.stream.Send(msg)
	}()

	// Wait for responses, send completion, or context cancellation
	for {
		select {
		case <-ctx.Done():
			log.Printf("[MessageBroker] [RequestId=%s] Context cancelled, Error=%v", requestId, ctx.Err())
			return nil, ctx.Err()
		case err := <-errCh:
			if err != nil {
				log.Printf("[MessageBroker] [RequestId=%s] ERROR: Failed to send request, Error=%v", requestId, err)
				return nil, err
			}
			log.Printf("[MessageBroker] [RequestId=%s] Request sent successfully, waiting for response", requestId)
		case resp, ok := <-ch:
			if !ok {
				log.Printf("[MessageBroker] [RequestId=%s] Channel closed (dispatcher likely stopped)", requestId)
				return nil, errors.New("channel closed by dispatcher")
			}

			log.Printf("[MessageBroker] [RequestId=%s] Received on channel, ResponseType=%T", requestId, resp)

			// Check if this is a progress message
			if mb.ops.IsProgressMessage(resp) {
				log.Printf("[MessageBroker] [RequestId=%s] Progress message", requestId)
				if onProgress != nil {
					progressText := mb.ops.GetProgressMessage(resp)
					if progressText != "" {
						onProgress(progressText)
					}
				}
				// Continue waiting for more messages
				continue
			}

			// Any non-progress message with matching RequestId is our final response
			log.Printf("[MessageBroker] [RequestId=%s] Received final response", requestId)
			if err := mb.ops.GetError(resp); err != nil {
				log.Printf("[MessageBroker] [RequestId=%s] Response contains error, Error=%v", requestId, err)
				return nil, err
			}
			return resp, nil
		}
	}
}

// Start starts a goroutine to receive and dispatch messages.
// Messages are routed based on RequestId (client pattern) or message type (server pattern).
func (mb *MessageBroker[TMessage]) Start(ctx context.Context) {
	go func() {
		log.Printf("[MessageBroker] Dispatcher started")
		for {
			select {
			case <-ctx.Done():
				log.Printf("[MessageBroker] Dispatcher stopped due to context cancellation")
				// Close all waiting channels to unblock callers
				mb.responseChans.Range(func(key, value any) bool {
					reqId := key.(string)
					ch := value.(chan *TMessage)
					log.Printf("[MessageBroker] Closing channel for RequestId=%s due to context cancellation", reqId)
					close(ch)
					return true
				})
				return
			default:
				resp, err := mb.stream.Recv()
				if err != nil {
					log.Printf("[MessageBroker] ERROR: stream.Recv() failed: %v", err)
					// propagate error to all waiting calls by closing channels
					mb.responseChans.Range(func(key, value any) bool {
						reqId := key.(string)
						ch := value.(chan *TMessage)
						log.Printf("[MessageBroker] Closing channel for RequestId=%s due to stream error", reqId)
						close(ch)
						return true
					})
					return
				}

				// Check if this is a progress message - always route to channel, never to handler
				if mb.ops.IsProgressMessage(resp) {
					requestId := mb.ops.GetRequestId(ctx, resp)
					log.Printf("[MessageBroker] Received progress message: RequestId=%s", requestId)
					if ch, ok := mb.responseChans.Load(requestId); ok {
						channelTyped := ch.(chan *TMessage)
						log.Printf("[MessageBroker] Dispatching progress message to channel for RequestId=%s", requestId)
						channelTyped <- resp
					} else {
						log.Printf("[MessageBroker] WARNING: No channel found for progress message RequestId=%s", requestId)
					}
					continue
				}

				requestId := mb.ops.GetRequestId(ctx, resp)
				log.Printf("[MessageBroker] Dispatcher received message: RequestId=%s, Type=%T", requestId, resp)

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

						log.Printf("[MessageBroker] Dispatching message to channel for RequestId=%s", requestId)
						channelTyped <- resp
						log.Printf("[MessageBroker] Message dispatched successfully to RequestId=%s", requestId)
						continue
					}
				}

				// No channel found, try to route to handler (server pattern - incoming request)
				innerMsg := mb.ops.GetInnerMessage(resp)
				if innerMsg == nil {
					log.Printf("[MessageBroker] WARNING: No inner message found for RequestId=%s", requestId)
					continue
				}

				msgType := reflect.TypeOf(innerMsg)
				if handlerVal, ok := mb.handlers.Load(msgType); ok {
					wrapper := handlerVal.(*handlerWrapper)
					log.Printf(
						"[MessageBroker] Dispatching to handler for message type: %v (RequestId=%s)",
						msgType,
						requestId,
					)

					// Invoke handler in serial execution (simpler, matches existing patterns)
					responseEnvelope := mb.invokeHandler(ctx, wrapper, resp, innerMsg)
					if responseEnvelope != nil {
						if err := mb.stream.Send(responseEnvelope); err != nil {
							log.Printf("[MessageBroker] ERROR: Failed to send handler response: %v", err)
						} else {
							log.Printf("[MessageBroker] Handler response sent successfully for RequestId=%s", requestId)
						}
					}
				} else {
					log.Printf(
						"[MessageBroker] WARNING: No handler registered for message type %T "+
							"(RequestId=%s) - message dropped",
						innerMsg,
						requestId,
					)
				}
			}
		}
	}()
}

// invokeHandler calls the registered handler and sends the response envelope
func (mb *MessageBroker[TMessage]) invokeHandler(
	ctx context.Context,
	wrapper *handlerWrapper,
	envelope *TMessage,
	innerMsg any,
) *TMessage {
	requestId := mb.ops.GetRequestId(ctx, envelope)

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
	mb.ops.SetRequestId(ctx, responseEnvelope, requestId)

	if handlerErr != nil {
		// Auto-set error on envelope
		log.Printf("[MessageBroker] Handler returned error for RequestId=%s: %v", requestId, handlerErr)
		mb.ops.SetError(responseEnvelope, handlerErr)
	}

	return responseEnvelope
}

// createProgressFunc creates a progress callback function for a given request ID
func (mb *MessageBroker[TMessage]) createProgressFunc(ctx context.Context, requestId string) ProgressFunc {
	return func(message string) {
		log.Printf("[MessageBroker] Sending progress for RequestId=%s: %s", requestId, message)

		// Create progress envelope using the envelope's factory method
		progressEnvelope := mb.ops.CreateProgressMessage(requestId, message)

		// Send the progress message on the stream
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
