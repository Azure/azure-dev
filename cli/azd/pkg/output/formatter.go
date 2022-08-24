// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"context"
	"fmt"
	"io"

	"github.com/mattn/go-colorable"
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

func WithFormatter(ctx context.Context, formatter Formatter) context.Context {
	return context.WithValue(ctx, formatterContextKey, formatter)
}

func GetFormatter(ctx context.Context) Formatter {
	formatter, ok := ctx.Value(formatterContextKey).(Formatter)
	if !ok {
		return &NoneFormatter{}
	}

	return formatter
}

func WithWriter(ctx context.Context, writer io.Writer) context.Context {
	return context.WithValue(ctx, writerContextKey, writer)
}

func GetWriter(ctx context.Context) io.Writer {
	writer, ok := ctx.Value(writerContextKey).(io.Writer)
	if !ok {
		return colorable.NewColorableStdout()
	}

	return writer
}
