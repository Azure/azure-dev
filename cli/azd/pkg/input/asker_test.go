// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/AlecAivazis/survey/v2"
	"github.com/stretchr/testify/require"
)

func Test_askOneNoPrompt_Input(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		defaultVal  string
		wantResult  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "WithDefault",
			message:    "Enter name:",
			defaultVal: "Alice",
			wantResult: "Alice",
		},
		{
			name:        "NoDefault",
			message:     "Enter name:",
			defaultVal:  "",
			wantErr:     true,
			errContains: "no default response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := &survey.Input{
				Message: tt.message,
				Default: tt.defaultVal,
			}
			var result string
			err := askOneNoPrompt(prompt, &result)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantResult, result)
			}
		})
	}
}

func Test_askOneNoPrompt_Select_IntResponse(t *testing.T) {
	tests := []struct {
		name        string
		options     []string
		defaultVal  any
		wantIdx     int
		wantErr     bool
		errContains string
	}{
		{
			name:       "DefaultInList",
			options:    []string{"a", "b", "c"},
			defaultVal: "b",
			wantIdx:    1,
		},
		{
			name:        "DefaultNotInList",
			options:     []string{"a", "b", "c"},
			defaultVal:  "missing",
			wantErr:     true,
			errContains: "default response not in list",
		},
		{
			name:        "NilDefault",
			options:     []string{"a"},
			defaultVal:  nil,
			wantErr:     true,
			errContains: "no default response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := &survey.Select{
				Message: "Pick one:",
				Options: tt.options,
				Default: tt.defaultVal,
			}
			var result int
			err := askOneNoPrompt(prompt, &result)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantIdx, result)
			}
		})
	}
}

func Test_askOneNoPrompt_Select_StringResponse(t *testing.T) {
	prompt := &survey.Select{
		Message: "Pick:",
		Options: []string{"x", "y"},
		Default: "y",
	}
	var result string
	err := askOneNoPrompt(prompt, &result)

	require.NoError(t, err)
	require.Equal(t, "y", result)
}

func Test_askOneNoPrompt_Select_BadResponseType(t *testing.T) {
	prompt := &survey.Select{
		Message: "Pick:",
		Options: []string{"x"},
		Default: "x",
	}
	var result float64
	err := askOneNoPrompt(prompt, &result)

	require.Error(t, err)
	require.Contains(t, err.Error(), "bad type")
}

func Test_askOneNoPrompt_Confirm(t *testing.T) {
	tests := []struct {
		name       string
		defaultVal bool
		wantResult bool
	}{
		{"DefaultTrue", true, true},
		{"DefaultFalse", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := &survey.Confirm{
				Message: "Continue?",
				Default: tt.defaultVal,
			}
			var result bool
			err := askOneNoPrompt(prompt, &result)

			require.NoError(t, err)
			require.Equal(t, tt.wantResult, result)
		})
	}
}

func Test_askOneNoPrompt_MultiSelect(t *testing.T) {
	tests := []struct {
		name        string
		options     []string
		defaultVal  any
		wantResult  []string
		wantErr     bool
		errContains string
	}{
		{
			name:       "WithDefaults",
			options:    []string{"a", "b", "c"},
			defaultVal: []string{"a", "c"},
			wantResult: []string{"a", "c"},
		},
		{
			name:        "NilDefault",
			options:     []string{"a"},
			defaultVal:  nil,
			wantErr:     true,
			errContains: "no default response",
		},
		{
			name:        "WrongDefaultType",
			options:     []string{"a"},
			defaultVal:  "not-a-slice",
			wantErr:     true,
			errContains: "not a string list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := &survey.MultiSelect{
				Message: "Pick many:",
				Options: tt.options,
				Default: tt.defaultVal,
			}
			var result []string
			err := askOneNoPrompt(prompt, &result)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantResult, result)
			}
		})
	}
}

func Test_askOneNoPrompt_UnknownType_Panics(t *testing.T) {
	prompt := &survey.Password{Message: "Secret:"}
	var result string

	require.Panics(t, func() {
		_ = askOneNoPrompt(prompt, &result)
	})
}

func Test_NewAsker_NoPrompt(t *testing.T) {
	asker := NewAsker(true, false, nil, nil)
	prompt := &survey.Confirm{
		Message: "OK?",
		Default: true,
	}
	var result bool
	err := asker(prompt, &result)

	require.NoError(t, err)
	require.True(t, result)
}

func Test_NewAsker_NonTerminal_Input(t *testing.T) {
	input := "Alice\n"
	r := strings.NewReader(input)
	w := &bytes.Buffer{}

	asker := NewAsker(false, false, w, r)
	prompt := &survey.Input{
		Message: "Name:",
	}
	var result string
	err := asker(prompt, &result)

	require.NoError(t, err)
	require.Equal(t, "Alice", result)
}

func Test_NewAsker_NonTerminal_InputWithDefault(t *testing.T) {
	// Empty input should use default
	input := "\n"
	r := strings.NewReader(input)
	w := &bytes.Buffer{}

	asker := NewAsker(false, false, w, r)
	prompt := &survey.Input{
		Message: "Name:",
		Default: "Bob",
	}
	var result string
	err := asker(prompt, &result)

	require.NoError(t, err)
	require.Equal(t, "Bob", result)
}

func Test_NewAsker_NonTerminal_Confirm(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   bool
		defVal bool
	}{
		{"Yes", "Y\n", true, false},
		{"No", "n\n", false, true},
		{"EmptyDefaultTrue", "\n", true, true},
		{"EmptyDefaultFalse", "\n", false, false},
		{"LowercaseY", "y\n", true, false},
		{"UppercaseN", "N\n", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			w := &bytes.Buffer{}

			asker := NewAsker(false, false, w, r)
			prompt := &survey.Confirm{
				Message: "Continue?",
				Default: tt.defVal,
			}
			result := tt.defVal
			err := asker(prompt, &result)

			require.NoError(t, err)
			require.Equal(t, tt.want, result)
		})
	}
}

func Test_NewAsker_NonTerminal_Select(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		options     []string
		defaultVal  any
		wantIdx     int
		wantErr     bool
		errContains string
	}{
		{
			name:    "ExactMatch",
			input:   "beta\n",
			options: []string{"alpha", "beta", "gamma"},
			wantIdx: 1,
		},
		{
			name:       "EmptyUsesDefault",
			input:      "\n",
			options:    []string{"alpha", "beta"},
			defaultVal: "alpha",
			wantIdx:    0,
		},
		{
			name:        "InvalidChoice",
			input:       "missing\n",
			options:     []string{"a", "b"},
			wantErr:     true,
			errContains: "not an allowed choice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.input)
			w := &bytes.Buffer{}

			asker := NewAsker(false, false, w, r)
			prompt := &survey.Select{
				Message: "Pick:",
				Options: tt.options,
				Default: tt.defaultVal,
			}
			var result int
			err := asker(prompt, &result)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantIdx, result)
			}
		})
	}
}

func Test_NewAsker_NonTerminal_Password(t *testing.T) {
	r := strings.NewReader("s3cret\n")
	w := &bytes.Buffer{}

	asker := NewAsker(false, false, w, r)
	prompt := &survey.Password{
		Message: "Password:",
	}
	var result string
	err := asker(prompt, &result)

	require.NoError(t, err)
	require.Equal(t, "s3cret", result)
}

func Test_NewAsker_NonTerminal_UnknownType_Panics(t *testing.T) {
	asker := NewAsker(false, false, &bytes.Buffer{}, strings.NewReader(""))

	require.Panics(t, func() {
		var result string
		_ = asker(&survey.Editor{Message: "Edit:"}, &result)
	})
}

func Test_askOnePrompt_Select_StringResponse(t *testing.T) {
	r := strings.NewReader("beta\n")
	w := &bytes.Buffer{}

	prompt := &survey.Select{
		Message: "Pick:",
		Options: []string{"alpha", "beta"},
	}
	var result string
	err := askOnePrompt(prompt, &result, false, w, r)

	require.NoError(t, err)
	require.Equal(t, "beta", result)
}

func Test_askOnePrompt_Select_BadResponseType(t *testing.T) {
	r := strings.NewReader("alpha\n")
	w := &bytes.Buffer{}

	prompt := &survey.Select{
		Message: "Pick:",
		Options: []string{"alpha"},
	}
	var result float64
	err := askOnePrompt(prompt, &result, false, w, r)

	require.Error(t, err)
	require.Contains(t, err.Error(), "bad type")
}

func Test_askOnePrompt_MultiSelect_BadDefaultType(t *testing.T) {
	r := strings.NewReader("\n")
	w := &bytes.Buffer{}

	prompt := &survey.MultiSelect{
		Message: "Pick:",
		Options: []string{"a", "b"},
		Default: "not-a-slice",
	}
	var result []string
	err := askOnePrompt(prompt, &result, false, w, r)

	require.Error(t, err)
	require.Contains(t, err.Error(), "not a string list")
}

func Test_promptFromOptions(t *testing.T) {
	tests := []struct {
		name     string
		options  ConsoleOptions
		wantType string
	}{
		{
			name: "Password",
			options: ConsoleOptions{
				Message:    "Enter password:",
				IsPassword: true,
			},
			wantType: "*survey.Password",
		},
		{
			name: "RegularInput",
			options: ConsoleOptions{
				Message: "Enter name:",
			},
			wantType: "*survey.Input",
		},
		{
			name: "InputWithDefault",
			options: ConsoleOptions{
				Message:      "Enter name:",
				DefaultValue: "Alice",
			},
			wantType: "*survey.Input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := promptFromOptions(tt.options)
			got := fmt.Sprintf("%T", p)
			require.Equal(t, tt.wantType, got)

			if tt.wantType == "*survey.Input" {
				inp := p.(*survey.Input)
				require.Equal(t, tt.options.Message, inp.Message)
				if defStr, ok := tt.options.DefaultValue.(string); ok {
					require.Equal(t, defStr, inp.Default)
				}
			}
		})
	}
}

func Test_choicesFromOptions(t *testing.T) {
	tests := []struct {
		name          string
		options       ConsoleOptions
		wantLen       int
		wantFirstVal  string
		wantHasDetail bool
	}{
		{
			name: "WithDetails",
			options: ConsoleOptions{
				Options:       []string{"A", "B"},
				OptionDetails: []string{"detail-A", "detail-B"},
			},
			wantLen:       2,
			wantFirstVal:  "A",
			wantHasDetail: true,
		},
		{
			name: "WithoutDetails",
			options: ConsoleOptions{
				Options: []string{"X", "Y"},
			},
			wantLen:       2,
			wantFirstVal:  "X",
			wantHasDetail: false,
		},
		{
			name: "PartialDetails",
			options: ConsoleOptions{
				Options:       []string{"A", "B", "C"},
				OptionDetails: []string{"d-A"},
			},
			wantLen:       3,
			wantFirstVal:  "A",
			wantHasDetail: true,
		},
		{
			name: "EmptyDetailString",
			options: ConsoleOptions{
				Options:       []string{"A"},
				OptionDetails: []string{""},
			},
			wantLen:       1,
			wantFirstVal:  "A",
			wantHasDetail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choices := choicesFromOptions(tt.options)
			require.Len(t, choices, tt.wantLen)
			require.Equal(t, tt.wantFirstVal, choices[0].Value)
			if tt.wantHasDetail {
				require.NotNil(t, choices[0].Detail)
			} else {
				require.Nil(t, choices[0].Detail)
			}
		})
	}
}
