// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/schema"
)

// FlushWriter is an interface for writers that support flushing
type FlushWriter interface {
	io.Writer
	Flush() error
}

// FileLogger logs all agent actions to a file with automatic flushing
type FileLogger struct {
	writer FlushWriter
	file   *os.File // Keep reference to close file when needed
}

// FileLoggerOption represents an option for configuring FileLogger
type FileLoggerOption func(*FileLogger)

// NewFileLogger creates a new file logger that writes to the provided FlushWriter
func NewFileLogger(writer FlushWriter, opts ...FileLoggerOption) callbacks.Handler {
	fl := &FileLogger{
		writer: writer,
	}

	for _, opt := range opts {
		opt(fl)
	}

	return fl
}

// NewFileLoggerDefault creates a new file logger with default settings.
// Opens or creates "azd-agent-{date}.log" in the current working directory.
// Returns the logger and a cleanup function that should be called to close the file.
func NewFileLoggerDefault(opts ...FileLoggerOption) (*FileLogger, func() error, error) {
	// Create dated filename: azd-agent-2025-08-05.log
	dateStr := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("azd-agent-%s.log", dateStr)

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open log file: %w", err)
	}

	bufferedWriter := bufio.NewWriter(file)

	// Create a flush writer that flushes both the buffer and the file
	flushWriter := &fileFlushWriter{
		writer: bufferedWriter,
		file:   file,
	}

	fl := &FileLogger{
		writer: flushWriter,
		file:   file,
	}

	for _, opt := range opts {
		opt(fl)
	}

	cleanup := func() error {
		if err := bufferedWriter.Flush(); err != nil {
			file.Close()
			return err
		}
		return file.Close()
	}

	return fl, cleanup, nil
}

// fileFlushWriter wraps a buffered writer and ensures both buffer and file are flushed
type fileFlushWriter struct {
	writer *bufio.Writer
	file   *os.File
}

func (fw *fileFlushWriter) Write(p []byte) (int, error) {
	return fw.writer.Write(p)
}

func (fw *fileFlushWriter) Flush() error {
	if err := fw.writer.Flush(); err != nil {
		return err
	}
	return fw.file.Sync()
}

// writeAndFlush writes a message to the file and flushes immediately
func (fl *FileLogger) writeAndFlush(format string, args ...interface{}) {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	message := fmt.Sprintf("[%s] %s\n", timestamp, fmt.Sprintf(format, args...))

	if _, err := fl.writer.Write([]byte(message)); err == nil {
		fl.writer.Flush()
	}
}

// HandleText is called when text is processed
func (fl *FileLogger) HandleText(ctx context.Context, text string) {
	fl.writeAndFlush("TEXT: %s", text)
}

// HandleLLMGenerateContentStart is called when LLM content generation starts
func (fl *FileLogger) HandleLLMGenerateContentStart(ctx context.Context, ms []llms.MessageContent) {
	fl.writeAndFlush("LLM_GENERATE_START: %d messages", len(ms))
}

// HandleLLMGenerateContentEnd is called when LLM content generation ends
func (fl *FileLogger) HandleLLMGenerateContentEnd(ctx context.Context, res *llms.ContentResponse) {
	for i, choice := range res.Choices {
		fl.writeAndFlush("LLM_GENERATE_END[%d]: %s", i, choice.Content)
	}
}

// HandleRetrieverStart is called when retrieval starts
func (fl *FileLogger) HandleRetrieverStart(ctx context.Context, query string) {
	fl.writeAndFlush("RETRIEVER_START: %s", query)
}

// HandleRetrieverEnd is called when retrieval ends
func (fl *FileLogger) HandleRetrieverEnd(ctx context.Context, query string, documents []schema.Document) {
	fl.writeAndFlush("RETRIEVER_END: query=%s, documents=%d", query, len(documents))
}

// HandleToolStart is called when a tool execution starts
func (fl *FileLogger) HandleToolStart(ctx context.Context, input string) {
	fl.writeAndFlush("TOOL_START: %s", input)
}

// HandleToolEnd is called when a tool execution ends
func (fl *FileLogger) HandleToolEnd(ctx context.Context, output string) {
	fl.writeAndFlush("TOOL_END: %s", output)
}

// HandleToolError is called when a tool execution fails
func (fl *FileLogger) HandleToolError(ctx context.Context, err error) {
	fl.writeAndFlush("TOOL_ERROR: %s", err.Error())
}

// HandleLLMStart is called when LLM call starts
func (fl *FileLogger) HandleLLMStart(ctx context.Context, prompts []string) {
	fl.writeAndFlush("LLM_START: %d prompts", len(prompts))
}

// HandleChainStart is called when chain execution starts
func (fl *FileLogger) HandleChainStart(ctx context.Context, inputs map[string]any) {
	inputsJson, _ := json.Marshal(inputs)
	fl.writeAndFlush("CHAIN_START: %s", string(inputsJson))
}

// HandleChainEnd is called when chain execution ends
func (fl *FileLogger) HandleChainEnd(ctx context.Context, outputs map[string]any) {
	outputsJson, _ := json.Marshal(outputs)
	fl.writeAndFlush("CHAIN_END: %s", string(outputsJson))
}

// HandleChainError is called when chain execution fails
func (fl *FileLogger) HandleChainError(ctx context.Context, err error) {
	fl.writeAndFlush("CHAIN_ERROR: %s", err.Error())
}

// HandleAgentAction is called when an agent action is planned
func (fl *FileLogger) HandleAgentAction(ctx context.Context, action schema.AgentAction) {
	fl.writeAndFlush("AGENT_ACTION: tool=%s, input=%s", action.Tool, action.ToolInput)
}

// HandleAgentFinish is called when the agent finishes
func (fl *FileLogger) HandleAgentFinish(ctx context.Context, finish schema.AgentFinish) {
	fl.writeAndFlush("AGENT_FINISH: %s", finish.Log)
}

// HandleLLMError is called when LLM call fails
func (fl *FileLogger) HandleLLMError(ctx context.Context, err error) {
	fl.writeAndFlush("LLM_ERROR: %s", err.Error())
}

// HandleStreamingFunc handles streaming responses
func (fl *FileLogger) HandleStreamingFunc(ctx context.Context, chunk []byte) {
}
