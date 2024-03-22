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
func FromAlphabet(alphabet string, length uint) (string, error) {
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

// GenerateConfig are the settings to control the output of calling Generate.
type GenerateConfig struct {
	Length     uint  `json:"length,omitempty"`
	NoLower    *bool `json:"noLower,omitempty"`
	NoUpper    *bool `json:"noUpper,omitempty"`
	NoNumeric  *bool `json:"noNumeric,omitempty"`
	NoSpecial  *bool `json:"noSpecial,omitempty"`
	MinLower   *uint `json:"minLower,omitempty"`
	MinUpper   *uint `json:"minUpper,omitempty"`
	MinNumeric *uint `json:"minNumeric,omitempty"`
	MinSpecial *uint `json:"minSpecial,omitempty"`
}

// Generate generates a password based on the provided configuration.
// It takes an `azure.AutoGenInput` configuration as input and returns the generated password as a string.
// If any error occurs during the generation process, it returns an error.
func Generate(config GenerateConfig) (string, error) {
	var minLower uint
	if config.MinLower != nil {
		minLower = *config.MinLower
	}
	var minUpper uint
	if config.MinUpper != nil {
		minUpper = *config.MinUpper
	}
	var minNumeric uint
	if config.MinNumeric != nil {
		minNumeric = *config.MinNumeric
	}
	var minSpecial uint
	if config.MinSpecial != nil {
		minSpecial = *config.MinSpecial
	}

	// a cluster is a group of characters that are required to be present in the password
	clustersLength := minLower + minUpper + minNumeric + minSpecial
	totalLength := config.Length
	if totalLength == 0 {
		totalLength = clustersLength
	}
	if clustersLength > totalLength {
		return "",
			fmt.Errorf("the sum of MinLower, MinUpper, MinNumeric, and MinSpecial must be less than or equal to the length")
	}
	if totalLength == 0 {
		return "", fmt.Errorf(
			"either Length or the sum of MinLower, MinUpper, MinNumeric, and MinSpecial must be greater than 0")
	}

	unassignedClusterSize := totalLength - clustersLength
	var generated string

	genCluster := func(minClusterSize uint, disallowedCluster *bool, alphabet string, appendTo *string) error {
		if disallowedCluster != nil && *disallowedCluster {
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
			*appendTo += gen
		}
		return nil
	}
	if err := genCluster(minLower, config.NoLower, LowercaseLetters, &generated); err != nil {
		return "", err
	}
	if err := genCluster(minUpper, config.NoUpper, UppercaseLetters, &generated); err != nil {
		return "", err
	}
	if err := genCluster(minNumeric, config.NoNumeric, Digits, &generated); err != nil {
		return "", err
	}
	if err := genCluster(minSpecial, config.NoSpecial, Symbols, &generated); err != nil {
		return "", err
	}

	// Strategy for generating remaining characters:
	// 1. If all characters are disallowed, return an error
	// 2. For each character that needs to be generated, generate a charset picking one random char for each allowed cluster
	// 3. Use the generated charset to pick a random character for the char
	// This strategy gives the same changes for each character to be picked from any cluster, regardless of the cluster size
	// For example, picking a char from lowercase letters and numbers should not give more changes to get a lowercase letter
	// just because there are more lower case letters than numbers.
	for unassignedClusterSize > 0 {
		var combinedAlphabet string
		var noDisallow bool
		if config.NoLower == nil || (config.NoLower != nil && !*config.NoLower) {
			if err := genCluster(1, &noDisallow, LowercaseLetters, &combinedAlphabet); err != nil {
				return "", err
			}
		}
		if config.NoUpper == nil || (config.NoUpper != nil && !*config.NoUpper) {
			if err := genCluster(1, &noDisallow, UppercaseLetters, &combinedAlphabet); err != nil {
				return "", err
			}
		}
		if config.NoNumeric == nil || (config.NoNumeric != nil && !*config.NoNumeric) {
			if err := genCluster(1, &noDisallow, Digits, &combinedAlphabet); err != nil {
				return "", err
			}
		}
		if config.NoSpecial == nil || (config.NoSpecial != nil && !*config.NoSpecial) {
			if err := genCluster(1, &noDisallow, Symbols, &combinedAlphabet); err != nil {
				return "", err
			}
		}
		if combinedAlphabet == "" {
			return "", fmt.Errorf("can't generate if all characters are disallowed (noLower, noUpper, noNumeric, noSpecial)")
		}
		if err := genCluster(1, &noDisallow, combinedAlphabet, &generated); err != nil {
			return "", err
		}
		unassignedClusterSize--
	}

	fixedSizeClustersStringChars := strings.Split(generated, "")
	if err := Shuffle(fixedSizeClustersStringChars); err != nil {
		return "", fmt.Errorf("shuffling fixed size cluster: %w", err)
	}

	return strings.Join(fixedSizeClustersStringChars, ""), nil
}
