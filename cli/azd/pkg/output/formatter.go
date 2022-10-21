// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"context"
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

type contextKey string

const (
	formatterContextKey contextKey = "formatter"
	writerContextKey    contextKey = "writer"
)

// Sets the output formatter that will be used to format data back to std out
func WithFormatter(ctx context.Context, formatter Formatter) context.Context {
	return context.WithValue(ctx, formatterContextKey, formatter)
}

// Gets the formatter that had been previously specified.
// If not found will default to `None` formatter.
func GetFormatter(ctx context.Context) Formatter {
	formatter, ok := ctx.Value(formatterContextKey).(Formatter)
	if !ok {
		return &NoneFormatter{}
	}

	return formatter
}

// Sets the io.Writer implementation used for printing to std out
func WithWriter(ctx context.Context, writer io.Writer) context.Context {
	return context.WithValue(ctx, writerContextKey, writer)
}

// Gets the io.Writer implementation previously specified or panics
// if one can not be found
func GetWriter(ctx context.Context) io.Writer {
	writer, ok := ctx.Value(writerContextKey).(io.Writer)
	if !ok {
		panic("no writer")
	}

	return writer
}
