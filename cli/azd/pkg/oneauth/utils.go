// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build oneauth

package oneauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

const applicationID = "com.microsoft.azd"

// started tracks whether the bridge's Startup function has succeeded. This is necessary
// because OneAuth returns an error when its Startup function is called more than once.
var started atomic.Bool

// writeDynamicLib writes data to path if path doesn't exist or its content doesn't match
// cmakeChecksum (the output of "cmake -E sha256sum").
func writeDynamicLib(path string, data []byte, cmakeChecksum string) error {
	if b, err := os.ReadFile(path); err == nil {
		hash, _, found := strings.Cut(cmakeChecksum, " ")
		if !found {
			return fmt.Errorf("malformed checksum %q", cmakeChecksum)
		}
		expected, err := hex.DecodeString(hash)
		if err != nil {
			return err
		}
		if actual := sha256.Sum256(b); bytes.Equal(expected, actual[:]) {
			return nil
		}
	}
	err := os.MkdirAll(filepath.Dir(path), 0700)
	if err == nil {
		err = os.WriteFile(path, data, 0600)
	}
	return err
}
