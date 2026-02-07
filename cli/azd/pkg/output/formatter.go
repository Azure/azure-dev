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

// Queryable is an optional interface that a Formatter may implement to support
// JMESPath filtering on arbitrary objects. Console messages (e.g. error output)
// use this to apply --query without going through the full Format() pipeline.
type Queryable interface {
	QueryFilter(obj interface{}) (interface{}, error)
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
