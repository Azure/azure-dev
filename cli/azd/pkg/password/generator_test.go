// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package password

import (
	"strings"
	"testing"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/stretchr/testify/require"
)

func TestZeroLengthPasswordError(t *testing.T) {
	_, err := Generate(GenerateConfig{})
	require.Error(t, err)
}

func TestOneCharPassword(t *testing.T) {
	// Really weak, so not practically usable, but we should be able to generate these 1-char passwords without errors

	var pwd string
	var err error

	pwd, err = Generate(GenerateConfig{MinLower: to.Ptr[uint](1)})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, LowercaseLetters))

	pwd, err = Generate(GenerateConfig{MinUpper: to.Ptr[uint](1)})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, UppercaseLetters))

	pwd, err = Generate(GenerateConfig{MinNumeric: to.Ptr[uint](1)})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, Digits))

	pwd, err = Generate(GenerateConfig{MinSpecial: to.Ptr[uint](1)})
	require.NoError(t, err)
	require.Len(t, pwd, 1)
	require.Equal(t, 1, countCharsFrom(pwd, Symbols))
}

func TestPasswordContainsRequestedChars(t *testing.T) {
	pwd, err := Generate(GenerateConfig{
		MinLower:   to.Ptr[uint](3),
		MinUpper:   to.Ptr[uint](4),
		MinNumeric: to.Ptr[uint](5),
		MinSpecial: to.Ptr[uint](6),
	})
	require.NoError(t, err)

	require.Equal(t, 3, countCharsFrom(pwd, LowercaseLetters))
	require.Equal(t, 4, countCharsFrom(pwd, UppercaseLetters))
	require.Equal(t, 5, countCharsFrom(pwd, Digits))
	require.Equal(t, 6, countCharsFrom(pwd, Symbols))
}

func TestPasswordAllDisallowed(t *testing.T) {
	pwd, err := Generate(GenerateConfig{
		Length:    10,
		NoLower:   to.Ptr(true),
		NoUpper:   to.Ptr(true),
		NoNumeric: to.Ptr(true),
		NoSpecial: to.Ptr(true),
	})
	require.ErrorContains(t, err, "can't generate if all characters are disallowed (noLower, noUpper, noNumeric, noSpecial)")
	require.Equal(t, "", pwd)
}

func TestPasswordShuffled(t *testing.T) {
	pwd, err := Generate(GenerateConfig{MinLower: to.Ptr[uint](10), MinUpper: to.Ptr[uint](20)})
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

func TestGenerateInput(t *testing.T) {
	config := GenerateConfig{
		Length:     8,
		NoSpecial:  to.Ptr(true),
		MinLower:   to.Ptr[uint](2),
		MinUpper:   to.Ptr[uint](2),
		MinNumeric: to.Ptr[uint](2),
	}

	expectedLength := 8
	expectedMinLower := 2
	expectedMinUpper := 2
	expectedMinNumeric := 2
	expectedMinSpecial := 0

	result, err := Generate(config)
	require.NoError(t, err)
	require.Equal(t, expectedLength, len(result))

	lowerCount := 0
	upperCount := 0
	numericCount := 0
	specialCount := 0

	for _, char := range result {
		if unicode.IsLower(char) {
			lowerCount++
		} else if unicode.IsUpper(char) {
			upperCount++
		} else if unicode.IsDigit(char) {
			numericCount++
		} else {
			specialCount++
		}
	}

	require.LessOrEqual(t, expectedMinLower, lowerCount)
	require.LessOrEqual(t, expectedMinUpper, upperCount)
	require.LessOrEqual(t, expectedMinNumeric, numericCount)
	require.LessOrEqual(t, expectedMinSpecial, specialCount)
}
