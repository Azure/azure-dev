package messaging

import "context"

type MessageFilter func(msg *Message) bool

type MessageHandler func(msg *Message)

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

func (s *Subscription) receive(msg *Message) {
	if s.filter != nil && !s.filter(msg) {
		return
	}

	s.handler(msg)
}

func (s *Subscription) Close(ctx context.Context) {
	s.topic.Unsubscribe(ctx, s)
}
