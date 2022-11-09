// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"
)

type Format string

const (
	EnvVarsFormat Format = "dotenv"
	JsonFormat    Format = "json"
	TableFormat   Format = "table"
	NoneFormat    Format = "none"
)

type Formatter interface {
	Kind() Format
	Format(obj interface{}, writer io.Writer, opts interface{}) error
}

func NewFormatter(format string) (Formatter, error) {
	switch format {
	case string(JsonFormat):
		return &JsonFormatter{}, nil
	case string(EnvVarsFormat):
		return &EnvVarsFormatter{}, nil
	case string(TableFormat):
		return &TableFormatter{}, nil
	case string(NoneFormat):
		return &NoneFormatter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format %v", format)
	}
}
