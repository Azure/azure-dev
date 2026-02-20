// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
)

const ExtensionErrorFileEnv = "AZD_ERROR_FILE"

// ReportError writes a structured extension error to the path provided in AZD_ERROR_FILE.
// It is a no-op when the environment variable is not set.
func ReportError(err error) error {
	path := os.Getenv(ExtensionErrorFileEnv)
	if path == "" {
		return nil
	}

	return WriteErrorFile(path, err)
}

// WriteErrorFile writes a structured extension error to a file path.
func WriteErrorFile(path string, err error) error {
	if err == nil || path == "" {
		return nil
	}

	extErr := WrapError(err)
	if extErr == nil {
		return nil
	}

	content, marshalErr := protojson.Marshal(extErr)
	if marshalErr != nil {
		return fmt.Errorf("marshal extension error: %w", marshalErr)
	}

	if writeErr := os.WriteFile(path, content, 0o600); writeErr != nil {
		return fmt.Errorf("write extension error file: %w", writeErr)
	}

	return nil
}

// ReadErrorFile reads and parses a structured extension error from file.
// Returns (nil, nil) when the file does not exist or is empty.
func ReadErrorFile(path string) (error, error) {
	if path == "" {
		return nil, nil
	}

	content, readErr := os.ReadFile(path)
	if errors.Is(readErr, os.ErrNotExist) {
		return nil, nil
	}
	if readErr != nil {
		return nil, fmt.Errorf("read extension error file: %w", readErr)
	}

	if len(content) == 0 {
		return nil, nil
	}

	msg := &ExtensionError{}
	if unmarshalErr := protojson.Unmarshal(content, msg); unmarshalErr != nil {
		return nil, fmt.Errorf("unmarshal extension error file: %w", unmarshalErr)
	}

	return UnwrapError(msg), nil
}
