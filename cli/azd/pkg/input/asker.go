package input

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/mattn/go-isatty"
)

type Asker func(p survey.Prompt, response interface{}) error

func NewAsker(noPrompt bool) Asker {
	if noPrompt {
		return askOneNoPrompt
	}

	return askOnePrompt
}

func askOneNoPrompt(p survey.Prompt, response interface{}) error {
	switch v := p.(type) {
	case *survey.Input:
		if v.Default == "" {
			return fmt.Errorf("no default response for prompt '%s'", v.Message)
		}

		*(response.(*string)) = v.Default
	case *survey.Select:
		if v.Default == nil {
			return fmt.Errorf("no default response for prompt '%s'", v.Message)
		}

		switch ptr := response.(type) {
		case *int:
			didSet := false
			for idx, item := range v.Options {
				if v.Default.(string) == item {
					*ptr = idx
					didSet = true
				}
			}

			if !didSet {
				return fmt.Errorf("default response not in list of options for prompt '%s'", v.Message)
			}
		case *string:
			*ptr = v.Default.(string)
		default:
			return fmt.Errorf("bad type %T for result, should be (*int or *string)", response)
		}
	case *survey.Confirm:
		*(response.(*bool)) = v.Default
	default:
		panic(fmt.Sprintf("don't know how to prompt for type %T", p))
	}

	return nil
}

func withShowCursor(o *survey.AskOptions) error {
	o.PromptConfig.ShowCursor = true
	return nil
}

func askOnePrompt(p survey.Prompt, response interface{}) error {
	// Like (*bufio.Reader).ReadString(byte) except that it does not buffer input from the input stream.
	// instead, it reads a byte at a time until a delimiter is found, without consuming any extra characters.
	readStringNoBuffer := func(r io.Reader, delim byte) (string, error) {
		strBuf := bytes.Buffer{}
		readBuf := make([]byte, 1)
		for {
			if _, err := r.Read(readBuf); err != nil {
				return strBuf.String(), err
			}

			// discard err, per documentation, WriteByte always succeeds.
			_ = strBuf.WriteByte(readBuf[0])

			if readBuf[0] == delim {
				return strBuf.String(), nil
			}
		}
	}

	if isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()) && os.Getenv("AZD_DEBUG_FORCE_NO_TTY") != "1" {
		opts := []survey.AskOpt{}

		// When asking a question which requires a text response, show the cursor, it helps
		// users understand we need some input.
		if _, ok := p.(*survey.Input); ok {
			opts = append(opts, withShowCursor)
		}

		return survey.AskOne(p, response, opts...)
	}

	switch v := p.(type) {
	case *survey.Input:
		var pResponse = response.(*string)
		fmt.Printf("%s", v.Message[0:len(v.Message)-1])
		if v.Default != "" {
			fmt.Printf(" (or hit enter to use the default %s)", v.Default)
		}
		fmt.Printf("%s ", v.Message[len(v.Message)-1:])
		result, err := readStringNoBuffer(os.Stdin, '\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading response: %w", err)
		}
		result = strings.TrimSpace(result)
		if result == "" && v.Default != "" {
			result = v.Default
		}
		*pResponse = result
		return nil
	case *survey.Select:
		for {
			fmt.Printf("%s", v.Message[0:len(v.Message)-1])
			if v.Default != nil {
				fmt.Printf(" (or hit enter to use the default %v)", v.Default)
			}
			fmt.Printf("%s ", v.Message[len(v.Message)-1:])
			result, err := readStringNoBuffer(os.Stdin, '\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading response: %w", err)
			}
			result = strings.TrimSpace(result)
			if result == "" && v.Default != nil {
				result = v.Default.(string)
			}
			for idx, val := range v.Options {
				if val == result {
					switch ptr := response.(type) {
					case *string:
						*ptr = val
					case *int:
						*ptr = idx
					default:
						return fmt.Errorf("bad type %T for result, should be (*int or *string)", response)
					}

					return nil
				}
			}
			fmt.Printf("error: %s is not an allowed choice\n", result)
		}
	case *survey.Confirm:
		var pResponse = response.(*bool)

		for {
			fmt.Print(v.Message)
			if *pResponse {
				fmt.Print(" (Y/n)")
			} else {
				fmt.Printf(" (y/N)")
			}
			result, err := readStringNoBuffer(os.Stdin, '\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading response: %w", err)
			}
			switch strings.TrimSpace(result) {
			case "Y", "y":
				*pResponse = true
				return nil
			case "N", "n":
				*pResponse = false
				return nil
			case "":
				return nil
			}
		}
	default:
		panic(fmt.Sprintf("don't know how to prompt for type %T", p))
	}
}
