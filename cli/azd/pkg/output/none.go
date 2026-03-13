// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"
)

type NoneFormatter struct{}

func (f *NoneFormatter) Kind() Format {
	return NoneFormat
}

func (f *NoneFormatter) Format(obj any, writer io.Writer, opts any) error {
	return fmt.Errorf("attempted to output formatted data when 'none' was chosen as output format")
}

var _ Formatter = (*NoneFormatter)(nil)
