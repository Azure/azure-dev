package messaging

import (
	"context"

	"golang.org/x/exp/slices"
)

type Topic struct {
	Name        string
	channel     chan *Message
	subscribers []*Subscription
}

func NewTopic(name string) *Topic {
	return &Topic{
		Name:        name,
		channel:     make(chan *Message),
		subscribers: []*Subscription{},
	}
}

func (t *Topic) Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) *Subscription {
	subscription := NewSubscription(t, filter, handler)
	t.subscribers = append(t.subscribers, subscription)

	return subscription
}

func (t *Topic) Unsubscribe(ctx context.Context, subscription *Subscription) {
	index := slices.IndexFunc(t.subscribers, func(s *Subscription) bool {
		return s == subscription
	})

	if index < 0 {
		return
	}

	// Remove subscription from t.subscribers
	t.subscribers = append(t.subscribers[:index], t.subscribers[index+1:]...)
}

func (t *Topic) Send(ctx context.Context, msg *Message) {
	t.channel <- msg
}

func (t *Topic) Close(ctx context.Context) {
	for _, subscriber := range t.subscribers {
		subscriber.Close(ctx)
	}

	close(t.channel)
}

func (t *Topic) init(ctx context.Context) {
	go func() {
		for msg := range t.channel {
			for i := len(t.subscribers) - 1; i >= 0; i-- {
				t.subscribers[i].receive(ctx, msg)
			}
		}
	}()
}
