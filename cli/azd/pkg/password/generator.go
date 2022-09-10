// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package password

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const (
	LowercaseLetters = "abcdefghijklmnopqrstuvwxyz"
	UppercaseLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	Digits           = "0123456789"
	Symbols          = "~!@#$%^&*()_+`-={}|[]\\:\"<>?,./"
)

type PasswordComposition struct {
	NumLowercase, NumUppercase, NumDigits, NumSymbols uint
}

// Generate password consisting of given number of lowercase letters, uppercase letters, digits, and "symbol" characters.
func Generate(cmp PasswordComposition) (string, error) {
	length := cmp.NumLowercase + cmp.NumUppercase + cmp.NumDigits + cmp.NumSymbols
	if length == 0 {
		return "", fmt.Errorf("Empty passwords are insecure")
	}

	chars := make([]byte, length)
	var pos uint = 0
	var err error

	err = addRandomChars(chars, &pos, cmp.NumLowercase, LowercaseLetters)
	if err != nil {
		return "", err
	}
	err = addRandomChars(chars, &pos, cmp.NumUppercase, UppercaseLetters)
	if err != nil {
		return "", err
	}
	err = addRandomChars(chars, &pos, cmp.NumDigits, Digits)
	if err != nil {
		return "", err
	}
	err = addRandomChars(chars, &pos, cmp.NumSymbols, Symbols)
	if err != nil {
		return "", err
	}

	err = Shuffle(chars)
	if err != nil {
		return "", err
	}
	return string(chars), nil
}

func addRandomChars(buf []byte, pos *uint, count uint, choices string) error {
	var i uint
	for i = 0; i < count; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
		if err != nil {
			return err
		}
		buf[i+*pos] = choices[n.Int64()]
	}

	*pos += count
	return nil
}
