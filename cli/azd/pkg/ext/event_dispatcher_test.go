// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
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

func Test_Multiple_Errors_With_Suggestions(t *testing.T) {
	handler1 := func(ctx context.Context, args testEventArgs) error {
		return &internal.ErrorWithSuggestion{
			Err:        errors.New("Error1 for test"),
			Suggestion: "Suggestion1",
		}
	}
	handler2 := func(ctx context.Context, args testEventArgs) error {
		return &internal.ErrorWithSuggestion{
			Err:        errors.New("Error2 for test"),
			Suggestion: "Suggestion2",
		}
	}

	ed := NewEventDispatcher[testEventArgs](testEvent)
	err := ed.AddHandler(testEvent, handler1)
	require.NoError(t, err)
	err = ed.AddHandler(testEvent, handler2)
	require.NoError(t, err)

	err = ed.RaiseEvent(context.Background(), testEvent, testEventArgs{})
	require.Error(t, err)

	var errWithSuggestion *internal.ErrorWithSuggestion
	ok := errors.As(err, &errWithSuggestion)
	require.True(t, ok)

	require.Contains(t, errWithSuggestion.Error(), "Error1 for test")
	require.Contains(t, errWithSuggestion.Error(), "Error2 for test")

	suggestion := errWithSuggestion.Suggestion
	require.Contains(t, suggestion, "Suggestion1")
	require.Contains(t, suggestion, "Suggestion2")
}

type testEventArgs struct{}

const testEvent Event = "test"
