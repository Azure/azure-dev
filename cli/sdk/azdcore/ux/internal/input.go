package internal

import (
	"os"
	"os/signal"
	"syscall"
	"time"
	"unicode"

	"github.com/eiannone/keyboard"
)

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

func NewInput() *Input {
	return &Input{
		cursor:  NewCursor(os.Stdout),
		SigChan: make(chan os.Signal),
	}
}

func (i *Input) ResetValue() {
	i.value = []rune{}
}

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

	sigChan := make(chan os.Signal, 1)

	done := func() {
		signal.Stop(sigChan)
		close(sigChan)

		if err := keyboard.Close(); err != nil {
			panic(err)
		}
	}

	// Register for SIGINT (Ctrl+C) signal
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		_, ok := <-sigChan
		if ok {
			i.SigChan <- os.Interrupt
		}
	}()

	i.cursor.ShowCursor()
	i.value = []rune(config.InitialValue)

	go func() {
		defer keyboard.Close()

		for {
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
	}()

	return inputChan, done, nil
}
