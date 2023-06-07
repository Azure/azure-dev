package messaging

import (
	"context"
)

// Publisher is an interface for sending messages.
type Publisher interface {
	// Send sends a message to the topic specified in the context.
	Send(ctx context.Context, msg *Envelope) error
}

// Subscriber is an interface for receiving messages.
type Subscriber interface {
	// Subscribe subscribes to the topic specified in the context with the specified filter and handler.
	Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) (*Subscription, error)
	Unsubscribe(ctx context.Context, subscription *Subscription)
}

// Service is a messaging service for sending and receiving messages.
type Service struct {
	topics map[string]*Topic
}

// NewService creates a new messaging service.
func NewService() *Service {
	return &Service{
		topics: map[string]*Topic{},
	}
}

type contextKey string

const (
	defaultTopicName string     = "default"
	topicContextKey  contextKey = "messaging-topic"
)

// WithTopic returns a new context with the specified topic name.
// Calls to Send / Subscribe will use the topic name from the context.
func (s *Service) WithTopic(ctx context.Context, topicName string) context.Context {
	return context.WithValue(ctx, topicContextKey, topicName)
}

// Send sends a message to the topic specified in the context.
func (s *Service) Send(ctx context.Context, msg *Envelope) error {
	return s.Topic(ctx).Send(ctx, msg)
}

// Subscribe subscribes to the topic specified in the context with the specified filter and handler.
func (s *Service) Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) (*Subscription, error) {
	return s.Topic(ctx).Subscribe(ctx, filter, handler)
}

func (s *Service) Unsubscribe(ctx context.Context, subscription *Subscription) {
	s.Topic(ctx).Unsubscribe(ctx, subscription)
}

// Topic returns the topic specified in the context.
// If no topic is specified in the context, the default topic is returned.
// If the topic does not exist, it is created.
func (s *Service) Topic(ctx context.Context) *Topic {
	topicName := s.getTopicName(ctx)
	topic, has := s.topics[topicName]
	if !has {
		topic = NewTopic(ctx, topicName)
		s.topics[topicName] = topic
	}

	return topic
}

func (s *Service) getTopicName(ctx context.Context) string {
	topicName, ok := ctx.Value(topicContextKey).(string)
	if !ok {
		topicName = defaultTopicName
	}

	return topicName
}
