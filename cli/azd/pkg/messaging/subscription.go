package messaging

import (
	"context"
	"fmt"
)

type MessageFilter func(ctx context.Context, msg *Message) bool

type MessageHandler func(ctx context.Context, msg *Message)

type Subscription struct {
	topic   *Topic
	filter  MessageFilter
	handler MessageHandler
}

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

func (s *Subscription) Close(ctx context.Context) error {
	err := s.topic.Flush(ctx)
	if err != nil {
		return fmt.Errorf("closing subscription: %w", err)
	}

	s.topic.Unsubscribe(ctx, s)
	return nil
}

func (s *Subscription) Flush(ctx context.Context) error {
	return s.topic.Flush(ctx)
}

func (s *Subscription) receive(ctx context.Context, msg *Message) {
	if s.filter != nil && !s.filter(ctx, msg) {
		return
	}

	s.handler(ctx, msg)
}
