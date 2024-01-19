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

// extractCMakeChecksum extracts a checksum from the output of "cmake -E sha256sum"
func extractCMakeChecksum(s string) ([]byte, error) {
	checksum, _, found := strings.Cut(s, " ")
	if !found {
		return nil, fmt.Errorf("malformed checksum %q", s)
	}
	return hex.DecodeString(checksum)
}

// writeDynamicLib writes data to path if path doesn't exist or its content doesn't match checksum
// (which is a SHA256 digest). The checksum is essentially a file version used to avoid unnecessary
// writes.
func writeDynamicLib(path string, data, checksum []byte) error {
	if b, err := os.ReadFile(path); err == nil {
		if actual := sha256.Sum256(b); bytes.Equal(actual[:], checksum) {
			return nil
		}
	}
	err := os.MkdirAll(filepath.Dir(path), 0700)
	if err == nil {
		err = os.WriteFile(path, data, 0600)
	}
	return err
}
