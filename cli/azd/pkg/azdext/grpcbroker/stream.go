// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcbroker

// BidiStream is a unified interface for bidirectional gRPC streams.
// Both grpc.BidiStreamingClient[T, T] and grpc.BidiStreamingServer[T, T]
// implement this interface, allowing the MessageBroker to work with either
// client-side or server-side streams.
type BidiStream[TMessage any] interface {
	// Send sends a message on the stream
	Send(msg *TMessage) error

	// Recv receives a message from the stream
	Recv() (*TMessage, error)
}
