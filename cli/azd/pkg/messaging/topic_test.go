package messaging

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Topic_Send(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.NoError(t, err)
	})

	t.Run("With_Nil_Message", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Send(ctx, nil)
		require.Error(t, err)
	})

	t.Run("After_Send", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.NoError(t, err)

		messages := []*Envelope{}
		// Subscribe after message is sent
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			messages = append(messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		// Subscription expected to be zero
		// Topics do not replay old messages
		require.Len(t, messages, 0)
	})

	t.Run("After_Close", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		topic.Close(ctx)

		// Topic was closed before sending messages. Expect error
		err := topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.Error(t, err)
	})

	t.Run("After_Unsubscribe", func(t *testing.T) {
		messages := []*Envelope{}
		topic := NewTopic(ctx, "test")

		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			messages = append(messages, msg)
		})

		require.NoError(t, err)
		require.NotNil(t, subscription)

		topic.Unsubscribe(ctx, subscription)

		// Subscription was unsubscribed before sending messages.
		// Not expecting any error but no messages should be received
		err = topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.NoError(t, err)
		require.Len(t, messages, 0)
	})
}

func Test_Topic_Receive(t *testing.T) {
	ctx := context.Background()

	t.Run("Single_Subscriber", func(t *testing.T) {
		messages := []*Envelope{}

		topic := NewTopic(ctx, "test")

		sub, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			messages = append(messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, sub)

		err = topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.NoError(t, err)

		err = topic.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, messages, 1)
	})

	t.Run("Multiple_Subscibers", func(t *testing.T) {
		sub1Messages := []*Envelope{}
		sub2Messages := []*Envelope{}

		topic := NewTopic(ctx, "test")

		sub1, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			sub1Messages = append(sub1Messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, sub1)

		sub2, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			sub2Messages = append(sub2Messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, sub2)

		// Sending a single message should be received across all subscriptions
		err = topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
		require.NoError(t, err)

		err = topic.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, sub1Messages, 1)
		require.Len(t, sub2Messages, 1)
	})
}

func Test_Topic_Subscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {})
		require.NoError(t, err)
		require.NotNil(t, subscription)
	})

	t.Run("With_Nil_Handler", func(t *testing.T) {
		topic := NewTopic(ctx, "test")

		// Subscription Handler is nil. Expect error
		_, err := topic.Subscribe(ctx, nil, nil)
		require.Error(t, err)
	})

	t.Run("With_Closed_Topic", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		topic.Close(ctx)

		// Topic was closed before subscribing. Expect error
		_, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {})
		require.Error(t, err)
	})
}

func Test_Topic_Unsubscibe(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		topic.Unsubscribe(ctx, subscription)
		require.NoError(t, err)
	})

	t.Run("With_Closed_Topic", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		err = topic.Close(ctx)
		require.NoError(t, err)
		topic.Unsubscribe(ctx, subscription)
	})
}

func Test_Topic_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Close(ctx)
		require.NoError(t, err)
	})

	t.Run("With_Closed_Topic", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Close(ctx)
		require.NoError(t, err)

		// Topic was already closed. Expect error
		err = topic.Close(ctx)
		require.Error(t, err)
	})
}
