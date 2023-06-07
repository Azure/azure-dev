package messaging

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Subscription_Receive(t *testing.T) {
	ctx := context.Background()

	t.Run("All_Messages", func(t *testing.T) {
		messages := []*Envelope{}
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			messages = append(messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		for i := 0; i < 10; i++ {
			err := topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
			require.NoError(t, err)
		}

		err = subscription.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, messages, 10)
	})

	t.Run("With_Message_Filter", func(t *testing.T) {
		messages := []*Envelope{}
		topic := NewTopic(ctx, "test")
		subscription, err := topic.Subscribe(ctx, func(ctx context.Context, msg *Envelope) bool {
			return msg.Type == SimpleMessage
		}, func(ctx context.Context, msg *Envelope) {
			messages = append(messages, msg)
		})
		require.NoError(t, err)
		require.NotNil(t, subscription)

		var customMessage MessageKind = "custom"

		for i := 0; i < 10; i++ {
			err := topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
			require.NoError(t, err)

			err = topic.Send(ctx, NewEnvelope(customMessage, "custom"))
			require.NoError(t, err)
		}

		err = subscription.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, messages, 10)
	})
}

func Test_Subscription_Close(t *testing.T) {
	ctx := context.Background()

	messages := []*Envelope{}
	topic := NewTopic(ctx, "test")
	subscription, err := topic.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
		messages = append(messages, msg)
	})
	require.NoError(t, err)
	require.NotNil(t, subscription)

	err = subscription.Close(ctx)
	require.NoError(t, err)

	err = topic.Send(ctx, NewEnvelope(SimpleMessage, "test"))
	require.NoError(t, err)

	err = subscription.Flush(ctx)
	require.NoError(t, err)

	require.Len(t, messages, 0)
}
