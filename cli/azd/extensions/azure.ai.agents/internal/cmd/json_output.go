// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func emitJSON(value any) error {
	return emitJSONTo(os.Stdout, value)
}

func emitJSONTo(out io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON output: %w", err)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
