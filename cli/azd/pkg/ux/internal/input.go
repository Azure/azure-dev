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
	"time"
	"unicode"

	"github.com/eiannone/keyboard"
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
	Key       keyboard.Key
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

	// Open the keyboard - sometimes it fails when a keyboard instance in in the process of closing.
	tries := 0

	for {
		if !keyboard.IsStarted(100 * time.Millisecond) {
			if err := keyboard.Open(); err != nil {
				tries++
				continue
			}
		}

		log.Printf("Keyboard opened successfully after %d tries\n", tries)
		break
	}

	// Start listening for key presses
	// We need to do this on a separate goroutine to avoid blocking the main thread.
	// To ensure we can still handle Ctrl+C or context cancellations.
	go func() {
		defer func() {
			if err := keyboard.Close(); err != nil {
				log.Printf("Error closing keyboard: %v\n", err)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case <-receiveChan:
				char, key, err := keyboard.GetKey()
				if err != nil {
					errChan <- err
					return
				}

				eventArgs := KeyPressEventArgs{
					Char: char,
					Key:  key,
				}

				if len(i.value) > 0 && (key == keyboard.KeyBackspace || key == keyboard.KeyBackspace2) {
					i.value = i.value[:len(i.value)-1]
				} else if !config.IgnoreHintKeys && char == '?' {
					eventArgs.Hint = true
				} else if !config.IgnoreHintKeys && key == keyboard.KeyEsc {
					eventArgs.Hint = false
				} else if key == keyboard.KeySpace {
					i.value = append(i.value, ' ')
				} else if unicode.IsPrint(char) {
					i.value = append(i.value, char)
				} else if key == keyboard.KeyCtrlC || key == keyboard.KeyCtrlX || key == keyboard.KeyEsc {
					eventArgs.Cancelled = true
					cancel()
					break
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
