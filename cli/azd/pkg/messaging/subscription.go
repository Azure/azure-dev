package messaging

import "context"

type MessageFilter func(ctx context.Context, msg *Message) bool

type MessageHandler func(ctx context.Context, msg *Message)

type Subscription struct {
	topic   *Topic
	filter  MessageFilter
	handler MessageHandler
}

func NewSubscription(topic *Topic, filter MessageFilter, handler MessageHandler) *Subscription {
	return &Subscription{
		topic:   topic,
		filter:  filter,
		handler: handler,
	}
}

func (s *Subscription) receive(ctx context.Context, msg *Message) {
	if s.filter != nil && !s.filter(ctx, msg) {
		return
	}

	s.handler(ctx, msg)
}

func (s *Subscription) Close(ctx context.Context) {
	s.topic.Unsubscribe(ctx, s)
}
