// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package password

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZeroLengthPasswordError(t *testing.T) {
	_, err := Generate(PasswordComposition{})
	require.Error(t, err)
}

func TestOneCharPassword(t *testing.T) {
	// Really weak, so not practically usable, but we should be able to generate these 1-char passwords without errors

	var pwd string
	var err error

	pwd, err = Generate(PasswordComposition{NumLowercase: 1})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, LowercaseLetters))

	pwd, err = Generate(PasswordComposition{NumUppercase: 1})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, UppercaseLetters))

	pwd, err = Generate(PasswordComposition{NumDigits: 1})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, Digits))

	pwd, err = Generate(PasswordComposition{NumSymbols: 1})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, Symbols))
}

func TestPasswordContainsRequestedChars(t *testing.T) {
	pwd, err := Generate(PasswordComposition{
		NumLowercase: 3,
		NumUppercase: 4,
		NumDigits:    5,
		NumSymbols:   6,
	})
	require.NoError(t, err)

	require.Equal(t, 3, countCharsFrom(pwd, LowercaseLetters))
	require.Equal(t, 4, countCharsFrom(pwd, UppercaseLetters))
	require.Equal(t, 5, countCharsFrom(pwd, Digits))
	require.Equal(t, 6, countCharsFrom(pwd, Symbols))
}

func TestPasswordShuffled(t *testing.T) {
	pwd, err := Generate(PasswordComposition{NumLowercase: 10, NumUppercase: 20})
	require.NoError(t, err)

	// Should be super improbable for the lowercase letters to remain at the front
	require.Less(t, countCharsFrom(string(pwd[0:10]), LowercaseLetters), 10)
}

func countCharsFrom(s, choices string) int {
	count := 0
	for i := 0; i < len(choices); i++ {
		count += strings.Count(s, string(choices[i]))
	}
	return count
}
