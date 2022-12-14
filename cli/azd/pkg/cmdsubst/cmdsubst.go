// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdsubst

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type CommandExecutor interface {
	// Returns true + replacement string if a command is recognized and runs successfully.
	// Returns false and no error if the command was not recognized by the executor, or in other "no-op" cases.
	Run(ctx context.Context, commandName string, args []string) (bool, string, error)
}

// This package is designed to be used in the context of ARM parameter file templates,
// which are relatively simple, JSON-format documents.
// The package relies on regular expressions and does not include a full parser.
//
// The command invocation is dollar sign, followed by open parenthesis and optional whitespace
// followed by command name (word chars), followed by optional argument list
// (word chars, whitespace, and dashes), followed by closing parenthesis.
var commandInvocationRegex = regexp.MustCompile(`\$\(\s*(\w+)([\w\s-]*)\)`)

// Similar to commandInvocationRegex, this format string is used to construct a regular expression
// that tests whether specific command is being invoked by a given document.
// We are looking for dollar sign, followed by open parenthesis and optional whitespace
// followed by command name (which will be filled in by the fmt.Sprintf() call),
// followed by optional argument list (word chars, whitespace, and dashes), followed by closing parenthesis.
const commandInvocationFmt = "\\$\\(\\s*%s[\\w\\s-]*\\)"

const (
	wholeMatchStart  = 0
	wholeMatchEnd    = 1
	commandNameStart = 2
	commandNameEnd   = 3
	argsStart        = 4
	argsEnd          = 5
)

// Eval replaces all occurrences of bash-like command output substitution $(command arg1 arg2 ...)
// with the result provided by the command executor.
// Any error from the command executor will result in an error reported from Eval().
func Eval(ctx context.Context, input string, cmd CommandExecutor) (string, error) {
	var sb strings.Builder

	allMatches := commandInvocationRegex.FindAllStringSubmatchIndex(input, -1)
	if len(allMatches) == 0 {
		return input, nil // No substitution necessary
	}

	contentBeforeMatchIndex := 0

	for _, match := range allMatches {
		// Write "content before match"
		sb.WriteString(input[contentBeforeMatchIndex:match[wholeMatchStart]])
		contentBeforeMatchIndex = match[wholeMatchEnd]

		// Extract invocation data and call evaluator
		commandName := input[match[commandNameStart]:match[commandNameEnd]]
		argumentStr := input[match[argsStart]:match[argsEnd]]
		args := strings.Fields(strings.TrimSpace(argumentStr))

		ran, result, err := cmd.Run(ctx, commandName, args)
		if err != nil {
			return "", err
		} else if ran {
			// Successful substitution
			sb.WriteString(result)
		} else {
			// Unrecognized command--write original content in and continue
			sb.WriteString(input[match[wholeMatchStart]:match[wholeMatchEnd]])
		}
	}

	// Write content after last match
	restStartIndex := allMatches[len(allMatches)-1][wholeMatchEnd]
	sb.WriteString(input[restStartIndex:])

	return sb.String(), nil
}

// Returns true if the document 'doc' contains an invocation of a command.
func ContainsCommandInvocation(doc, commandName string) bool {
	if len(commandName) == 0 || len(doc) == 0 {
		return false
	}
	regexStr := fmt.Sprintf(commandInvocationFmt, commandName)
	commandInvocationRegex := regexp.MustCompile(regexStr)
	return commandInvocationRegex.MatchString(doc)
}
