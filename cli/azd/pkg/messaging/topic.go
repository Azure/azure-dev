package messaging

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/exp/slices"
)

type Topic struct {
	Name        string
	channel     chan *Message
	subscribers []*Subscription
	flushLock   sync.Mutex
	closed      bool
}

func NewTopic(ctx context.Context, name string) *Topic {
	topic := &Topic{
		Name:        name,
		channel:     make(chan *Message),
		subscribers: []*Subscription{},
	}

	topic.init(ctx)
	return topic
}

func (t *Topic) Subscribe(ctx context.Context, filter MessageFilter, handler MessageHandler) (*Subscription, error) {
	if err := t.ensureOpen(); err != nil {
		return nil, err
	}

	subscription, err := NewSubscription(t, filter, handler)
	if err != nil {
		return nil, err
	}

	t.subscribers = append(t.subscribers, subscription)

	return subscription, nil
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

func (t *Topic) Send(ctx context.Context, msg *Message) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}

	if msg == nil {
		return fmt.Errorf("sending message: message is nil")
	}

	t.channel <- msg
	return nil
}

func (t *Topic) Close(ctx context.Context) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}

	close(t.channel)
	t.Flush(ctx)

	for _, subscriber := range t.subscribers {
		subscriber.Close(ctx)
	}

	t.closed = true
	return nil
}

func (t *Topic) Flush(ctx context.Context) error {
	if err := t.ensureOpen(); err != nil {
		return err
	}

	t.flushLock.Lock()
	defer t.flushLock.Unlock()

	return nil
}

func (t *Topic) receive(ctx context.Context, msg *Message) {
	// Attempt to re-lock the flush mutex
	_ = t.flushLock.TryLock()
	for i := len(t.subscribers) - 1; i >= 0; i-- {
		t.subscribers[i].receive(ctx, msg)
	}
}

func (t *Topic) init(ctx context.Context) {
	go func() {
		flushed := false
		t.flushLock.Lock()

		for {
			select {
			case <-ctx.Done():
				t.Close(ctx)
				if !flushed {
					t.flushLock.Unlock()
				}
				return
			case msg := <-t.channel:
				flushed = false
				t.receive(ctx, msg)
			default:
				if !flushed {
					t.flushLock.Unlock()
					flushed = true
				}
			}
		}
	}()
}

func (t *Topic) ensureOpen() error {
	if t.closed {
		return fmt.Errorf("topic has already been closed")
	}

	return nil
}
