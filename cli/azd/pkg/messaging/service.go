package messaging

import (
	"context"
)

type Publisher interface {
	Send(ctx context.Context, msg *Message) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) (*Subscription, error)
}

type Service struct {
	topics map[string]*Topic
}

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

func (s *Service) WithTopic(ctx context.Context, topicName string) context.Context {
	return context.WithValue(ctx, topicContextKey, topicName)
}

func (s *Service) Send(ctx context.Context, msg *Message) error {
	return s.Topic(ctx).Send(ctx, msg)
}

func (s *Service) Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) (*Subscription, error) {
	return s.Topic(ctx).Subscribe(ctx, filter, handler)
}

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
