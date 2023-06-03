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
		err := topic.Send(ctx, NewMessage(SimpleMessage, "test"))
		require.NoError(t, err)
	})

	t.Run("With_Nil_Message", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Send(ctx, nil)
		require.Error(t, err)
	})

	t.Run("After_Send", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		err := topic.Send(ctx, NewMessage(SimpleMessage, "test"))
		require.NoError(t, err)

		messages := []*Message{}
		// Subscribe after message is sent
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {
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

		err := topic.Send(ctx, NewMessage(SimpleMessage, "test"))
		require.Error(t, err)
	})

	t.Run("After_Unsubscribe", func(t *testing.T) {
		messages := []*Message{}
		topic := NewTopic(ctx, "test")

		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {
			messages = append(messages, msg)
		})

		require.NoError(t, err)
		require.NotNil(t, subscription)

		topic.Unsubscribe(ctx, subscription)

		err = topic.Send(ctx, NewMessage(SimpleMessage, "test"))
		require.NoError(t, err)
		require.Len(t, messages, 0)
	})

	t.Run("With_Closed_Context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctx)

		topic := NewTopic(ctx, "test")
		cancel()
		err := topic.Flush(ctx)
		require.NoError(t, err)

		require.Panics(t, func() {
			_ = topic.Send(ctx, NewMessage(SimpleMessage, "test"))
		})
	})
}

func Test_Topic_Subscribe(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {})
		require.NoError(t, err)
		require.NotNil(t, subscription)
	})

	t.Run("With_Nil_Handler", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		_, err := topic.Subscribe(ctx, nil, nil)
		require.Error(t, err)
	})

	t.Run("With_Closed_Topic", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		topic.Close(ctx)

		_, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {})
		require.Error(t, err)
	})
}

func Test_Topic_Unsubscibe(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		topic.Unsubscribe(ctx, subscription)
		require.NoError(t, err)
	})

	t.Run("With_Closed_Topic", func(t *testing.T) {
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Message) {})
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

		err = topic.Close(ctx)
		require.Error(t, err)
	})
}
