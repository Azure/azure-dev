package messaging

import (
	"context"
)

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

func (s *Service) Send(ctx context.Context, msg *Message) {
	topic := s.ensureTopic(ctx)
	topic.Send(ctx, msg)
}

func (s *Service) Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) *Subscription {
	topic := s.ensureTopic(ctx)
	return topic.Subscribe(ctx, filter, handler)
}

func (s *Service) ensureTopic(ctx context.Context) *Topic {
	topicName := s.getTopicName(ctx)
	topic, has := s.topics[topicName]
	if !has {
		topic = NewTopic(topicName)
		topic.init(ctx)
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
