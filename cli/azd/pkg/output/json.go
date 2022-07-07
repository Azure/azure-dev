// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"encoding/json"
	"io"
)

type JsonFormatter struct {
}

func (f *JsonFormatter) Kind() Format {
	return JsonFormat
}

func (f *JsonFormatter) Format(obj interface{}, writer io.Writer, _ interface{}) error {
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}

	_, err = writer.Write(b)
	if err != nil {
		return err
	}

	_, err = writer.Write([]byte("\n"))
	if err != nil {
		return err
	}

	return nil
}

var _ Formatter = (*JsonFormatter)(nil)
