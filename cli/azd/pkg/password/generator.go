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
	var fixedSizeClustersString string
	var dynamicSizeClustersString string

	genCluster := func(minClusterSize uint, disallowedCluster *bool, alphabet string) error {
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
	if err := genCluster(minLower, config.NoLower, LowercaseLetters); err != nil {
		return "", err
	}
	if err := genCluster(minUpper, config.NoUpper, UppercaseLetters); err != nil {
		return "", err
	}
	if err := genCluster(minNumeric, config.NoNumeric, Digits); err != nil {
		return "", err
	}
	if err := genCluster(minSpecial, config.NoSpecial, Symbols); err != nil {
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
