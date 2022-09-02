// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package password

import (
	"crypto/rand"
	"math/big"
)

// Fisher-Yates shuffle
func Shuffle[T any](s []T) error {
	N := len(s)
	for i := N - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1))) // from 0 to i, inclusive
		if err != nil {
			return err
		}
		j := n.Int64()
		s[j], s[i] = s[i], s[j]
	}
	return nil
}
