// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/eiannone/keyboard"
)

// Input is a base component for UX components that require user input.
type Input struct {
	cursor  Cursor
	value   []rune
	SigChan chan os.Signal
}

type InputEventArgs struct {
	Value string
	Char  rune
	Key   keyboard.Key
	Hint  bool
}

type InputConfig struct {
	InitialValue   string
	IgnoreHintKeys bool
}

// NewInput creates a new Input instance.
func NewInput() *Input {
	return &Input{
		cursor:  NewCursor(os.Stdout),
		SigChan: make(chan os.Signal, 1),
	}
}

// ResetValue resets the value of the input.
func (i *Input) ResetValue() {
	i.value = []rune{}
}

// ReadInput reads user input from the keyboard.
func (i *Input) ReadInput(config *InputConfig) (<-chan InputEventArgs, func(), error) {
	if config == nil {
		config = &InputConfig{}
	}

	inputChan := make(chan InputEventArgs)

	if !keyboard.IsStarted(200 * time.Millisecond) {
		if err := keyboard.Open(); err != nil {
			return nil, nil, err
		}
	}

	done := func() {
		sync.OnceFunc(func() {
			signal.Stop(i.SigChan)

			if err := keyboard.Close(); err != nil {
				panic(err)
			}

		})()
	}

	// Register for SIGINT (Ctrl+C) signal
	signal.Notify(i.SigChan, syscall.SIGINT, syscall.SIGTERM)

	i.cursor.ShowCursor()
	i.value = []rune(config.InitialValue)

	go func() {
		defer keyboard.Close()

		for {
			select {
			case <-i.SigChan:
				done()
				return
			default:
				eventArgs := InputEventArgs{}
				char, key, err := keyboard.GetKey()
				if err != nil {
					break
				}

				eventArgs.Char = char
				eventArgs.Key = key

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
				} else if key == keyboard.KeyCtrlC || key == keyboard.KeyCtrlX {
					i.SigChan <- os.Interrupt
				}

				eventArgs.Value = string(i.value)
				inputChan <- eventArgs
			}
		}
	}()

	return inputChan, done, nil
}
