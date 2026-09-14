// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package definition

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// Load reads a connection definition from a JSON or YAML file.
func Load(path string) (*Definition, error) {
	// #nosec G304 -- reading a caller-supplied definition is the purpose of Load.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read connection definition %q: %w", path, err)
	}

	definition := &Definition{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(definition); err != nil {
			return nil, fmt.Errorf("decode connection definition %q: %w", path, err)
		}
		if err := requireEOF(decoder.Decode(new(any))); err != nil {
			return nil, fmt.Errorf("decode connection definition %q: %w", path, err)
		}
	case ".yaml", ".yml":
		decoder := yaml.NewDecoder(bytes.NewReader(content))
		decoder.KnownFields(true)
		if err := decoder.Decode(definition); err != nil {
			return nil, fmt.Errorf("decode connection definition %q: %w", path, err)
		}
		if err := requireEOF(decoder.Decode(new(any))); err != nil {
			return nil, fmt.Errorf("decode connection definition %q: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported connection definition file type %q", filepath.Ext(path))
	}

	return definition, nil
}

// Save writes a connection definition atomically in the format selected by path.
func Save(path string, definition *Definition) error {
	if definition == nil {
		return fmt.Errorf("connection definition must not be nil")
	}

	var (
		content []byte
		err     error
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		content, err = json.MarshalIndent(definition, "", "  ")
	case ".yaml", ".yml":
		content, err = yaml.Marshal(definition)
	default:
		return fmt.Errorf("unsupported connection definition file type %q", filepath.Ext(path))
	}
	if err != nil {
		return fmt.Errorf("encode connection definition %q: %w", path, err)
	}
	content = append(content, '\n')

	if err := azdext.WriteFileAtomic(path, content, 0); err != nil {
		return fmt.Errorf("write connection definition %q: %w", path, err)
	}
	return nil
}

func requireEOF(err error) error {
	if err == nil {
		return fmt.Errorf("multiple documents are not supported")
	}
	if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
