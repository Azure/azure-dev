package messaging

import (
	"context"
	"fmt"
)

// MessageFilter is a function that determines whether a message should be handled by a subscription.
type MessageFilter func(ctx context.Context, msg *Message) bool

// MessageHandler is a function that handles a message.
type MessageHandler func(ctx context.Context, msg *Message)

// Subscription is a subscription to a topic.
type Subscription struct {
	topic   *Topic
	filter  MessageFilter
	handler MessageHandler
}

// NewSubscription creates a new subscription to the specified topic with the specified filter and handler.
func NewSubscription(topic *Topic, filter MessageFilter, handler MessageHandler) (*Subscription, error) {
	if handler == nil {
		return nil, fmt.Errorf("creating subscription: handler is nil")
	}

	return &Subscription{
		topic:   topic,
		filter:  filter,
		handler: handler,
	}, nil
}

// Close closes the subscription.
func (s *Subscription) Close(ctx context.Context) error {
	err := s.topic.Flush(ctx)
	if err != nil {
		return fmt.Errorf("closing subscription: %w", err)
	}

	s.topic.Unsubscribe(ctx, s)
	return nil
}

// Flush flushes the subscription.
func (s *Subscription) Flush(ctx context.Context) error {
	return s.topic.Flush(ctx)
}

func (s *Subscription) receive(ctx context.Context, msg *Message) {
	if s.filter != nil && !s.filter(ctx, msg) {
		return
	}

	s.handler(ctx, msg)
}
