// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

// SessionEventLogger handles Copilot SDK session events for UX display and file logging.
type SessionEventLogger struct {
	thoughtChan chan<- Thought
}

// NewSessionEventLogger creates a new event logger that emits Thought structs
// to the provided channel based on Copilot SDK session events.
func NewSessionEventLogger(thoughtChan chan<- Thought) *SessionEventLogger {
	return &SessionEventLogger{
		thoughtChan: thoughtChan,
	}
}

// HandleEvent processes a Copilot SDK SessionEvent and emits corresponding Thought structs.
func (l *SessionEventLogger) HandleEvent(event copilot.SessionEvent) {
	if l.thoughtChan == nil {
		return
	}

	switch event.Type {
	case copilot.AssistantMessage:
		if event.Data.Content != nil && *event.Data.Content != "" {
			content := strings.TrimSpace(*event.Data.Content)
			if content != "" && !strings.Contains(strings.ToLower(content), "do i need to use a tool?") {
				l.thoughtChan <- Thought{
					Thought: content,
				}
			}
		}

	case copilot.ToolExecutionStart:
		toolName := ""
		if event.Data.ToolName != nil {
			toolName = *event.Data.ToolName
		} else if event.Data.MCPToolName != nil {
			toolName = *event.Data.MCPToolName
		}
		if toolName == "" {
			return
		}

		actionInput := extractToolInputSummary(event.Data.Arguments)
		l.thoughtChan <- Thought{
			Action:      toolName,
			ActionInput: actionInput,
		}

	case copilot.AssistantReasoning:
		if event.Data.ReasoningText != nil && *event.Data.ReasoningText != "" {
			l.thoughtChan <- Thought{
				Thought: strings.TrimSpace(*event.Data.ReasoningText),
			}
		}
	}
}

// extractToolInputSummary creates a short summary of tool arguments for display.
func extractToolInputSummary(args any) string {
	if args == nil {
		return ""
	}

	argsMap, ok := args.(map[string]any)
	if !ok {
		return ""
	}

	// Prioritize specific param keys for display
	prioritizedKeys := []string{"path", "pattern", "filename", "command"}
	for _, key := range prioritizedKeys {
		if val, exists := argsMap[key]; exists {
			s := fmt.Sprintf("%s: %v", key, val)
			return truncateString(s, 120)
		}
	}

	return ""
}

// SessionFileLogger logs all Copilot SDK session events to a daily log file.
type SessionFileLogger struct {
	file *os.File
}

// NewSessionFileLogger creates a file logger that writes session events to a daily log file.
// Returns the logger and a cleanup function to close the file.
func NewSessionFileLogger() (*SessionFileLogger, func() error, error) {
	logDir, err := getLogDir()
	if err != nil {
		return nil, func() error { return nil }, err
	}

	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, func() error { return nil }, fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile := filepath.Join(logDir, fmt.Sprintf("azd-agent-%s.log", time.Now().Format("2006-01-02")))
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, func() error { return nil }, fmt.Errorf("failed to open log file: %w", err)
	}

	logger := &SessionFileLogger{file: f}
	cleanup := func() error { return f.Close() }

	return logger, cleanup, nil
}

// HandleEvent writes a session event to the log file.
func (l *SessionFileLogger) HandleEvent(event copilot.SessionEvent) {
	if l.file == nil {
		return
	}

	timestamp := time.Now().Format(time.RFC3339)
	eventType := string(event.Type)

	var detail string
	switch event.Type {
	case copilot.ToolExecutionStart:
		toolName := ""
		if event.Data.ToolName != nil {
			toolName = *event.Data.ToolName
		}
		detail = fmt.Sprintf("tool=%s", toolName)
	case copilot.ToolExecutionComplete:
		toolName := ""
		if event.Data.ToolName != nil {
			toolName = *event.Data.ToolName
		}
		detail = fmt.Sprintf("tool=%s", toolName)
	case copilot.AssistantMessage:
		content := ""
		if event.Data.Content != nil {
			content = truncateString(*event.Data.Content, 200)
		}
		detail = fmt.Sprintf("content=%s", content)
	case copilot.SessionError:
		msg := ""
		if event.Data.Message != nil {
			msg = *event.Data.Message
		}
		detail = fmt.Sprintf("error=%s", msg)
	default:
		detail = eventType
	}

	line := fmt.Sprintf("[%s] %s: %s\n", timestamp, eventType, detail)
	//nolint:errcheck
	l.file.WriteString(line)
}

// CompositeEventHandler chains multiple session event handlers together.
type CompositeEventHandler struct {
	handlers []func(copilot.SessionEvent)
}

// NewCompositeEventHandler creates a handler that forwards events to all provided handlers.
func NewCompositeEventHandler(handlers ...func(copilot.SessionEvent)) *CompositeEventHandler {
	return &CompositeEventHandler{handlers: handlers}
}

// HandleEvent forwards the event to all registered handlers.
func (c *CompositeEventHandler) HandleEvent(event copilot.SessionEvent) {
	for _, h := range c.handlers {
		h(event)
	}
}

func getLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".azd", "logs"), nil
}
