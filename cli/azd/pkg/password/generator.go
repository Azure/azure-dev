// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package password

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const (
	LowercaseLetters = "abcdefghijklmnopqrstuvwxyz"
	UppercaseLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	Digits           = "0123456789"
	Symbols          = "~!@#$%^&*()_+`-={}|[]\\:\"<>?,./"
	LettersAndDigits = LowercaseLetters + UppercaseLetters + Digits
)

// FromAlphabet generates a password of a given length, using only characters from the given alphabet (which should
// be a string with no duplicates)
func FromAlphabet(alphabet string, length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("Empty passwords are insecure")
	}

	chars := make([]byte, length)
	var pos uint = 0
	if err := addRandomChars(chars, &pos, uint(length), alphabet); err != nil {
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

type GenerateConfig struct {
	MinLength  *int  `json:"minLength,omitempty"`
	Lower      *bool `json:"lower,omitempty"`
	Upper      *bool `json:"upper,omitempty"`
	Numeric    *bool `json:"numeric,omitempty"`
	Special    *bool `json:"special,omitempty"`
	MinLower   *int  `json:"minLower,omitempty"`
	MinUpper   *int  `json:"minUpper,omitempty"`
	MinNumeric *int  `json:"minNumeric,omitempty"`
	MinSpecial *int  `json:"minSpecial,omitempty"`
}

// Generate generates a password based on the provided configuration.
// It takes an `azure.AutoGenInput` configuration as input and returns the generated password as a string.
// If any error occurs during the generation process, it returns an error.
func Generate(config GenerateConfig) (string, error) {
	var minLength int
	if config.MinLength != nil {
		minLength = *config.MinLength
	}
	var minLower int
	if config.MinLower != nil {
		minLower = *config.MinLower
	}
	var minUpper int
	if config.MinUpper != nil {
		minUpper = *config.MinUpper
	}
	var minNumeric int
	if config.MinNumeric != nil {
		minNumeric = *config.MinNumeric
	}
	var minSpecial int
	if config.MinSpecial != nil {
		minSpecial = *config.MinSpecial
	}
	var lower bool
	if config.Lower != nil {
		lower = *config.Lower
	} else {
		lower = true
	}
	var upper bool
	if config.Upper != nil {
		upper = *config.Upper
	} else {
		upper = true
	}
	var numeric bool
	if config.Numeric != nil {
		numeric = *config.Numeric
	} else {
		numeric = true
	}
	var special bool
	if config.Special != nil {
		special = *config.Special
	} else {
		special = true
	}

	// a cluster is a group of characters that are required to be present in the password
	clustersSize := minLower + minUpper + minNumeric + minSpecial
	totalLength := minLength
	if clustersSize > totalLength {
		totalLength = clustersSize
	}
	if totalLength == 0 {
		return "", fmt.Errorf(
			"either minLength or the sum of minLower, minUpper, minNumeric, and minSpecial must be greater than 0")
	}

	unassignedClusterSize := totalLength - clustersSize
	var fixedSizeClustersString string
	var dynamicSizeClustersString string

	genCluster := func(minClusterSize int, condition bool, alphabet string) error {
		if !condition {
			if minClusterSize > 0 {
				return fmt.Errorf("cluster size is greater than 0 but the condition is false")
			}
			return nil
		}
		if minClusterSize > 0 {
			gen, err := FromAlphabet(alphabet, minClusterSize)
			if err != nil {
				return fmt.Errorf("generating fixed size cluster: %w", err)
			}
			fixedSizeClustersString += gen
		}
		if unassignedClusterSize > 0 {
			gen, err := FromAlphabet(alphabet, unassignedClusterSize)
			if err != nil {
				return fmt.Errorf("generating fixed size cluster: %w", err)
			}
			dynamicSizeClustersString += gen
		}
		return nil
	}
	if err := genCluster(minLower, lower, LowercaseLetters); err != nil {
		return "", err
	}
	if err := genCluster(minUpper, upper, UppercaseLetters); err != nil {
		return "", err
	}
	if err := genCluster(minNumeric, numeric, Digits); err != nil {
		return "", err
	}
	if err := genCluster(minSpecial, special, Symbols); err != nil {
		return "", err
	}

	dynamicSizeClusterChars := strings.Split(dynamicSizeClustersString, "")
	if err := Shuffle(dynamicSizeClusterChars); err != nil {
		return "", fmt.Errorf("shuffling dynamic size cluster: %w", err)
	}

	for unassignedClusterSize > 0 {
		fixedSizeClustersString += dynamicSizeClusterChars[unassignedClusterSize-1]
		unassignedClusterSize--
	}

	fixedSizeClustersStringChars := strings.Split(fixedSizeClustersString, "")
	if err := Shuffle(fixedSizeClustersStringChars); err != nil {
		return "", fmt.Errorf("shuffling fixed size cluster: %w", err)
	}

	return strings.Join(fixedSizeClustersStringChars, ""), nil
}
