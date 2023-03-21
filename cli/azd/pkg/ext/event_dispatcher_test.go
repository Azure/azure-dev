package ext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Valid_Event_Names(t *testing.T) {
	handler := func(ctx context.Context, args testEventArgs) error {
		return nil
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)
	err := ed.AddHandler(testEvent, handler)
	require.NoError(t, err)

	err = ed.RemoveHandler(testEvent, handler)
	require.NoError(t, err)

	err = ed.Invoke(context.Background(), testEvent, testEventArgs{}, func() error {
		return nil
	})

	require.NoError(t, err)
}

func Test_Invalid_Event_Names(t *testing.T) {
	handler := func(ctx context.Context, args testEventArgs) error {
		return nil
	}

	invalidEventName := Event("invalid")
	ed := NewEventDispatcher[testEventArgs](testEvent)

	tests := map[string]func() error{
		"AddHandler": func() error {
			return ed.AddHandler(invalidEventName, handler)
		},
		"RemoveHandler": func() error {
			return ed.RemoveHandler(invalidEventName, handler)
		},
		"Invoke": func() error {
			return ed.Invoke(context.Background(), invalidEventName, testEventArgs{}, func() error {
				return nil
			})
		},
	}

	for testName, fn := range tests {
		t.Run(testName, func(t *testing.T) {
			err := fn()
			require.ErrorIs(t, err, ErrInvalidEvent)
		})
	}
}

type testEventArgs struct{}

const testEvent Event = "test"
