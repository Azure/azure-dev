// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/internal"
)

type Format string

const (
	EnvVarsFormat Format = "dotenv"
	ExportFormat  Format = "export"
	JsonFormat    Format = "json"
	TableFormat   Format = "table"
	NoneFormat    Format = "none"
)

type Formatter interface {
	Kind() Format
	Format(obj interface{}, writer io.Writer, opts interface{}) error
}

func NewFormatter(format string, globalOptions *internal.GlobalCommandOptions) (Formatter, error) {
	switch format {
	case string(JsonFormat):
		return NewJsonFormatter(globalOptions), nil
	case string(EnvVarsFormat):
		return &EnvVarsFormatter{}, nil
	case string(ExportFormat):
		return &ExportFormatter{}, nil
	case string(TableFormat):
		return &TableFormatter{}, nil
	case string(NoneFormat):
		return &NoneFormatter{}, nil
	default:
		return nil, fmt.Errorf("unsupported format %v", format)
	}
}
