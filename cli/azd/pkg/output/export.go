// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"
)

type ExportFormatter struct {
}

func (f *ExportFormatter) Kind() Format {
	return ExportFormat
}

func (f *ExportFormatter) Format(obj interface{}, writer io.Writer, _ interface{}) error {
	values, ok := obj.(map[string]string)
	if !ok {
		return fmt.Errorf("ExportFormatter can only format objects of type map[string]string")
	}

	var content string
	for key, value := range values {
		content += fmt.Sprintf("export %s=%s\n", key, value)
	}

	_, err := writer.Write([]byte(content))
	if err != nil {
		return fmt.Errorf("could not write content: %w", err)
	}

	return nil
}

var _ Formatter = (*ExportFormatter)(nil)
