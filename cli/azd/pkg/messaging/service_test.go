package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Service_Send_Receive(t *testing.T) {
	ctx := context.Background()
	service := NewService()

	t.Run("With_Default_Topic", func(t *testing.T) {
		recievedMessages := []*Envelope{}

		subscription, err := service.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			recievedMessages = append(recievedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription.Close(ctx)

		err = service.Send(ctx, NewEnvelope(SimpleMessage, "Hello World"))
		require.NoError(t, err)
		subscription.Flush(ctx)

		require.Len(t, recievedMessages, 1)
	})

	t.Run("With_Custom_Topic", func(t *testing.T) {
		topicCtx := service.WithTopic(ctx, "custom")
		recievedMessages := []*Envelope{}

		subscription, err := service.Subscribe(topicCtx, nil, func(ctx context.Context, msg *Envelope) {
			recievedMessages = append(recievedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription.Close(topicCtx)

		subscription.Flush(topicCtx)

		err = service.Send(topicCtx, NewEnvelope(SimpleMessage, "Hello World"))
		require.NoError(t, err)
		require.Len(t, recievedMessages, 1)
	})

	t.Run("With_Multiple_Topics", func(t *testing.T) {
		topic1Ctx := service.WithTopic(ctx, "topic1")
		topic2Ctx := service.WithTopic(ctx, "topic2")

		topic1ReceivedMessages := []*Envelope{}
		topic2ReceivedMessages := []*Envelope{}

		subscription1, err := service.Subscribe(topic1Ctx, nil, func(ctx context.Context, msg *Envelope) {
			topic1ReceivedMessages = append(topic1ReceivedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription1.Close(topic1Ctx)

		subscription2, err := service.Subscribe(topic2Ctx, nil, func(ctx context.Context, msg *Envelope) {
			topic2ReceivedMessages = append(topic2ReceivedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription2.Close(topic2Ctx)

		err = service.Send(topic1Ctx, NewEnvelope(SimpleMessage, "Hello World"))
		require.NoError(t, err)

		err = service.Send(topic2Ctx, NewEnvelope(SimpleMessage, "Hello World"))
		require.NoError(t, err)

		err = subscription1.Flush(topic1Ctx)
		require.NoError(t, err)

		err = subscription2.Flush(topic2Ctx)
		require.NoError(t, err)

		require.Len(t, topic1ReceivedMessages, 1)
		require.Len(t, topic2ReceivedMessages, 1)
	})

	t.Run("With_Multiple_Messages", func(t *testing.T) {
		recievedMessages := []*Envelope{}

		subscription, err := service.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			recievedMessages = append(recievedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription.Close(ctx)

		messageCount := 100
		for i := 0; i < 100; i++ {
			err = service.Send(ctx, NewEnvelope(SimpleMessage, fmt.Sprintf("Hello World %d", i)))
			require.NoError(t, err)
		}

		err = subscription.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, recievedMessages, messageCount)
	})

	t.Run("With_More_Messages_After_Flush", func(t *testing.T) {
		recievedMessages := []*Envelope{}

		subscription, err := service.Subscribe(ctx, nil, func(ctx context.Context, msg *Envelope) {
			recievedMessages = append(recievedMessages, msg)
		})
		require.NoError(t, err)
		defer subscription.Close(ctx)

		messageCount := 100
		for i := 0; i < 100; i++ {
			err = service.Send(ctx, NewEnvelope(SimpleMessage, fmt.Sprintf("Hello World %d", i)))
			require.NoError(t, err)
		}

		err = subscription.Flush(ctx)
		require.NoError(t, err)

		for i := 0; i < 100; i++ {
			err = service.Send(ctx, NewEnvelope(SimpleMessage, fmt.Sprintf("Hello World %d", i)))
			require.NoError(t, err)
		}

		err = subscription.Flush(ctx)
		require.NoError(t, err)

		require.Len(t, recievedMessages, 2*messageCount)
	})
}
