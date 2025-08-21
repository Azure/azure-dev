// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package http

import (
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// HttpToolsLoader loads HTTP-related tools
type HttpToolsLoader struct {
	callbackHandler callbacks.Handler
}

func NewHttpToolsLoader(callbackHandler callbacks.Handler) *HttpToolsLoader {
	return &HttpToolsLoader{
		callbackHandler: callbackHandler,
	}
}

func (l *HttpToolsLoader) LoadTools() ([]tools.Tool, error) {
	return []tools.Tool{
		&HTTPFetcherTool{},
	}, nil
}
