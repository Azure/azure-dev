// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
)

type Asker func(p survey.Prompt, response interface{}) error

func NewAsker(noPrompt bool, isTerminal bool, w io.Writer, r io.Reader) Asker {
	if noPrompt {
		return askOneNoPrompt
	}

	return func(p survey.Prompt, response interface{}) error {
		return askOnePrompt(p, response, isTerminal, w, r)
	}
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
	case *survey.MultiSelect:
		if v.Default == nil {
			return fmt.Errorf("no default response for prompt '%s'", v.Message)
		}
		defValue, err := v.Default.([]string)
		if !err {
			return fmt.Errorf("default response type is not a string list '%s'", v.Message)
		}
		*(response.(*[]string)) = defValue
	default:
		panic(fmt.Sprintf("don't know how to prompt for type %T", p))
	}

	return nil
}

func withShowCursor(o *survey.AskOptions) error {
	o.PromptConfig.ShowCursor = true
	return nil
}

func askOnePrompt(p survey.Prompt, response interface{}, isTerminal bool, stdout io.Writer, stdin io.Reader) error {
	// Like (*bufio.Reader).ReadString(byte) except that it does not buffer input from the input stream.
	// Instead, it reads a byte at a time until a delimiter is found or EOF is encountered,
	// returning bytes read with no extra characters consumed.
	readStringNoBuffer := func(r io.Reader, delim byte) (string, error) {
		strBuf := bytes.Buffer{}
		readBuf := make([]byte, 1)
		for {
			bytesRead, err := r.Read(readBuf)
			if bytesRead > 0 {
				// discard err, per documentation, WriteByte always succeeds.
				_ = strBuf.WriteByte(readBuf[0])
			}

			if err != nil {
				return strBuf.String(), err
			}

			if readBuf[0] == delim {
				return strBuf.String(), nil
			}
		}
	}

	if isTerminal {
		opts := []survey.AskOpt{}

		// When asking a question which requires a text response, show the cursor, it helps
		// users understand we need some input.
		if _, ok := p.(*survey.Input); ok {
			opts = append(opts, withShowCursor)
		}

		opts = append(opts, survey.WithIcons(func(icons *survey.IconSet) {
			// use bold blue question mark for all questions
			icons.Question.Format = "blue+b"
			icons.SelectFocus.Format = "blue+b"

			icons.Help.Format = "black+h"
			icons.Help.Text = "Hint:"

			icons.MarkedOption.Text = "[" + color.GreenString("✓") + "]"
			icons.MarkedOption.Format = ""
		}))

		return survey.AskOne(p, response, opts...)
	}

	switch v := p.(type) {
	case *survey.Input:
		var pResponse = response.(*string)
		fmt.Fprintf(stdout, "%s", v.Message[0:len(v.Message)-1])
		if v.Default != "" {
			fmt.Fprintf(stdout, " (or hit enter to use the default %s)", v.Default)
		}
		fmt.Fprintf(stdout, "%s ", v.Message[len(v.Message)-1:])
		result, err := readStringNoBuffer(stdin, '\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading response: %w", err)
		}
		result = strings.TrimSpace(result)
		if result == "" && v.Default != "" {
			result = v.Default
		}
		*pResponse = result
		return nil
	case *survey.Password:
		var pResponse = response.(*string)
		fmt.Fprintf(stdout, "%s", v.Message)
		result, err := readStringNoBuffer(stdin, '\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading response: %w", err)
		}
		result = strings.TrimSpace(result)
		*pResponse = result
		return nil
	case *survey.MultiSelect:
		// For multi-selection, azd will do a Select for each item, using the default to control the Y or N
		defValue, err := v.Default.([]string)
		if !err {
			return fmt.Errorf("default response type is not a string list '%s'", v.Message)
		}
		fmt.Fprintf(stdout, "%s:", v.Message)
		selection := make([]string, 0, len(v.Options))
		for _, item := range v.Options {
			response := slices.Contains(defValue, item)
			err := askOnePrompt(&survey.Confirm{
				Message: fmt.Sprintf("\n  select %s?", item),
			}, &response, isTerminal, stdout, stdin)
			if err != nil {
				return err
			}
			confirmation := "N"
			if response {
				confirmation = "Y"
				selection = append(selection, item)
			}
			fmt.Fprintf(stdout, "  %s", confirmation)
		}
		// assign the selection to the response
		*(response.(*[]string)) = selection

		return nil
	case *survey.Select:
		fmt.Fprintf(stdout, "%s", v.Message[0:len(v.Message)-1])
		if v.Default != nil {
			fmt.Fprintf(stdout, " (or hit enter to use the default %v)", v.Default)
		}
		fmt.Fprintf(stdout, "%s ", v.Message[len(v.Message)-1:])
		result, err := readStringNoBuffer(stdin, '\n')
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
		return fmt.Errorf(
			"'%s' is not an allowed choice. allowed choices: %v",
			result,
			strings.Join(v.Options, ","))
	case *survey.Confirm:
		var pResponse = response.(*bool)

		for {
			fmt.Fprint(stdout, v.Message)
			if *pResponse {
				fmt.Fprint(stdout, " (Y/n)")
			} else {
				fmt.Fprintf(stdout, " (y/N)")
			}
			result, err := readStringNoBuffer(stdin, '\n')
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

func init() {
	// blue for everything

	// Customize the input question template:
	//   - Use blue instead of cyan for answers: {{- color "blue"}}{{.Answer}}
	//   - Use gray instead of cyan for default value: {{color "black+h"}}({{.Default}})
	//nolint:lll
	survey.InputQuestionTemplate = `
	{{- if .ShowHelp }}{{- color .Config.Icons.Help.Format }}{{ .Config.Icons.Help.Text }} {{ .Help }}{{color "reset"}}{{"\n"}}{{end}}
	{{- color .Config.Icons.Question.Format }}{{ .Config.Icons.Question.Text }} {{color "reset"}}
	{{- color "default+hb"}}{{ .Message }} {{color "reset"}}
	{{- if .ShowAnswer}}
	  {{- color "blue"}}{{.Answer}}{{color "reset"}}{{"\n"}}
	{{- else if .PageEntries -}}
	  {{- .Answer}} [Use arrows to move, enter to select, type to continue]
	  {{- "\n"}}
	  {{- range $ix, $choice := .PageEntries}}
		{{- if eq $ix $.SelectedIndex }}{{color $.Config.Icons.SelectFocus.Format }}{{ $.Config.Icons.SelectFocus.Text }} {{else}}{{color "default"}}  {{end}}
		{{- $choice.Value}}
		{{- color "reset"}}{{"\n"}}
	  {{- end}}
	{{- else }}
	  {{- if or (and .Help (not .ShowHelp)) .Suggest }}{{color "cyan"}}[
		{{- if and .Help (not .ShowHelp)}}{{ print .Config.HelpInput }} for help {{- if and .Suggest}}, {{end}}{{end -}}
		{{- if and .Suggest }}{{color "cyan"}}{{ print .Config.SuggestInput }} for suggestions{{end -}}
	  ]{{color "reset"}} {{end}}
	  {{- if .Default}}{{color "black+h"}}({{.Default}}) {{color "reset"}}{{end}}
	{{- end}}`
}
