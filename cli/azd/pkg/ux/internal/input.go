// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"unicode"

	surveyterm "github.com/AlecAivazis/survey/v2/terminal"
)

var ErrCancelled = errors.New("cancelled by user")

// Input is a base component for UX components that require user input.
type Input struct {
	cursor Cursor
	value  []rune
}

type KeyPressEventArgs struct {
	Value     string
	Char      rune
	Key       rune
	Hint      bool
	Cancelled bool
}

type InputConfig struct {
	InitialValue   string
	IgnoreHintKeys bool
}

// NewInput creates a new Input instance.
func NewInput() *Input {
	return &Input{
		cursor: NewCursor(os.Stdout),
	}
}

// KeyPressEventHandler is a function type that handles key press events.
// Return true to continue listening for key presses, false to stop.
type KeyPressEventHandler func(args *KeyPressEventArgs) (bool, error)

// ResetValue resets the value of the input.
func (i *Input) ResetValue() {
	i.value = []rune{}
}

// ReadInput reads user input from the keyboard.
func (i *Input) ReadInput(ctx context.Context, config *InputConfig, handler KeyPressEventHandler) error {
	if config == nil {
		config = &InputConfig{}
	}

	// Create a cancellable context to avoid leaking goroutines.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	i.cursor.ShowCursor()
	i.value = []rune(config.InitialValue)

	// Channel to receive errors from the keyboard input
	errChan := make(chan error, 1)

	// Channel to receive OS signals (e.g., Ctrl+C)
	signalChan := make(chan os.Signal, 1)

	// Channel to receive active key press events
	inputChan := make(chan *KeyPressEventArgs)

	// Signals that we should continue listening for key presses.
	receiveChan := make(chan struct{})

	// Register for SIGINT (Ctrl+C) signal
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	defer func() {
		signal.Stop(signalChan)
	}()

	stdio := surveyterm.Stdio{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	rr := surveyterm.NewRuneReader(stdio)

	// Start listening for key presses
	// We need to do this on a separate goroutine to avoid blocking the main thread.
	// To ensure we can still handle Ctrl+C or context cancellations.
	go func() {
		// Set terminal to raw mode for reading
		// This allows us to read all user inputs without the terminal processing them
		// (e.g., handling backspace, Ctrl+C, etc.).
		if err := rr.SetTermMode(); err != nil {
			errChan <- err
			return
		}
		defer func() {
			if err := rr.RestoreTermMode(); err != nil {
				log.Printf("Error restoring terminal mode: %v\n", err)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case <-receiveChan:
				// Read the next rune from the terminal
				char, _, err := rr.ReadRune()
				if err != nil {
					errChan <- err
					return
				}

				eventArgs := KeyPressEventArgs{
					Char: char,
					Key:  char,
				}

				if len(i.value) > 0 && (char == surveyterm.KeyBackspace || char == surveyterm.KeyDelete) {
					i.value = i.value[:len(i.value)-1]
				} else if !config.IgnoreHintKeys && char == '?' {
					eventArgs.Hint = true
				} else if !config.IgnoreHintKeys && char == surveyterm.KeyEscape {
					eventArgs.Hint = false
				} else if char == surveyterm.KeySpace {
					i.value = append(i.value, ' ')
				} else if unicode.IsPrint(char) {
					i.value = append(i.value, char)
				} else if char == surveyterm.KeyInterrupt || char == surveyterm.KeyEscape {
					eventArgs.Cancelled = true
					cancel()
					break
				} else if char == '\n' { // Handle both '\r' and '\n' as Enter key
					eventArgs.Key = surveyterm.KeyEnter
				}

				eventArgs.Value = string(i.value)
				inputChan <- &eventArgs
			}
		}
	}()

	// Start the main event loop
	receiveChan <- struct{}{}

	for {
		select {
		case err := <-errChan:
			return err
		case <-ctx.Done():
			// If cancellation comes from context, return cancellation error.
			allErrors := errors.Join(ErrCancelled, ctx.Err())
			args := KeyPressEventArgs{Cancelled: true}
			_, err := handler(&args)
			if err != nil {
				allErrors = errors.Join(allErrors, err)
			}

			return allErrors
		case <-signalChan:
			// On OS signal, cancel the context to notify the goroutine.
			cancel()

			allErrors := errors.Join(ErrCancelled)
			if ctx.Err() != nil {
				allErrors = errors.Join(allErrors, ctx.Err())
			}

			args := KeyPressEventArgs{Cancelled: true}
			_, err := handler(&args)
			if err != nil {
				allErrors = errors.Join(allErrors, err)
			}
			return allErrors
		case args, ok := <-inputChan:
			if !ok {
				return nil
			}

			keepListening, err := handler(args)
			if err != nil {
				return err
			}

			if !keepListening {
				return nil
			}

			receiveChan <- struct{}{}
		}
	}
}
