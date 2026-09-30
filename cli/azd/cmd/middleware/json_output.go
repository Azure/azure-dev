// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type JsonOutputMiddleware struct {
	options *Options
	writer  *output.JsonEventWriter
	console input.Console
}

type commandStartEvent struct {
	Command string `json:"command"`
}

type commandEndEvent struct {
	ExitCode int `json:"exitCode"`
}

func NewJsonOutputMiddleware(
	options *Options,
	writer *output.JsonEventWriter,
	console input.Console,
) Middleware {
	return &JsonOutputMiddleware{
		options: options,
		writer:  writer,
		console: console,
	}
}

func (m *JsonOutputMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.writer == nil || !m.writer.Enabled() || IsChildAction(ctx) {
		return next(ctx)
	}

	if err := m.writer.Open(); err != nil {
		m.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", err.Error()))
		return nil, err
	}

	if err := m.writer.Write(contracts.EventEnvelope{
		Type:      contracts.CommandStartEventDataType,
		Timestamp: time.Now(),
		Data:      commandStartEvent{Command: m.options.CommandPath},
	}); err != nil {
		_ = m.writer.Close()
		m.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", err.Error()))
		return nil, err
	}

	result, actionErr := next(ctx)
	_ = m.writer.FlushStreams()
	exitCode := 0
	if actionErr != nil {
		exitCode = 1
		if exitCodeErr, ok := errors.AsType[*internal.ExitCodeError](actionErr); ok &&
			exitCodeErr.ExitCode != 0 {
			exitCode = exitCodeErr.ExitCode
		}
	}

	_ = m.writer.Write(contracts.EventEnvelope{
		Type:      contracts.CommandEndEventDataType,
		Timestamp: time.Now(),
		Data:      commandEndEvent{ExitCode: exitCode},
	})
	_ = m.writer.Close()
	streamErr := m.writer.Err()
	if streamErr != nil {
		m.console.Message(ctx, output.WithErrorFormat("\nERROR: %s", streamErr.Error()))
	}

	return result, errors.Join(actionErr, streamErr)
}
