// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxResponsesSSEEventBytes = 4 * 1024 * 1024

type responsesStreamProgress struct {
	ResponseID string
	Cursor     *int64
	Status     string
	EventType  string
	Terminal   bool
}

type responsesEventEnvelope struct {
	Type           string          `json:"type"`
	SequenceNumber *int64          `json:"sequence_number"`
	Response       json.RawMessage `json:"response"`
}

type responsesSnapshot struct {
	ID             string                `json:"id"`
	ResponseID     string                `json:"response_id"`
	Status         string                `json:"status"`
	AgentSessionID string                `json:"agent_session_id"`
	Output         []responsesOutputItem `json:"output"`
	Error          *responsesError       `json:"error"`
}

type responsesOutputItem struct {
	Content []responsesContent `json:"content"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesSSEEvent struct {
	name string
	data []byte
}

func readResponsesSSE(
	ctx context.Context,
	body io.Reader,
	writer io.Writer,
	agentName string,
	requireTerminal bool,
	onProgress func(responsesStreamProgress) error,
) error {
	return readResponsesSSEWithLimit(
		ctx,
		body,
		writer,
		agentName,
		requireTerminal,
		onProgress,
		maxResponsesSSEEventBytes,
	)
}

func readResponsesSSEWithLimit(
	ctx context.Context,
	body io.Reader,
	writer io.Writer,
	agentName string,
	requireTerminal bool,
	onProgress func(responsesStreamProgress) error,
	maxEventBytes int,
) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxEventBytes+len("data: ")+1)

	var eventName string
	var eventData bytes.Buffer
	var dataSeen bool
	var printed bool
	var identity string
	var cursor *int64
	var status string
	var terminal bool

	dispatch := func() error {
		if !dataSeen {
			eventName = ""
			return nil
		}

		event := responsesSSEEvent{name: eventName, data: eventData.Bytes()}
		eventName = ""
		dataSeen = false
		defer eventData.Reset()

		var envelope responsesEventEnvelope
		if err := json.Unmarshal(event.data, &envelope); err != nil {
			if event.name == "error" {
				return fmt.Errorf("agent stream error: %s", event.data)
			}
			if !isKnownResponsesEvent(event.name) {
				return nil
			}
			return fmt.Errorf("decode Responses SSE event: %w", err)
		}
		if event.name == "" {
			event.name = envelope.Type
		}
		if envelope.SequenceNumber != nil && cursor != nil && *envelope.SequenceNumber <= *cursor {
			return nil
		}

		var snapshot responsesSnapshot
		if len(envelope.Response) > 0 {
			if err := json.Unmarshal(envelope.Response, &snapshot); err != nil {
				return fmt.Errorf("decode Responses snapshot: %w", err)
			}
			responseID := snapshot.ID
			if responseID == "" {
				responseID = snapshot.ResponseID
			}
			if identity != "" && responseID != "" && responseID != identity {
				return fmt.Errorf("Responses stream changed response ID from %q to %q", identity, responseID)
			}
			if responseID != "" {
				identity = responseID
			}
		}
		if snapshot.Status == "" {
			switch event.name {
			case "response.completed":
				snapshot.Status = "completed"
			case "response.failed":
				snapshot.Status = "failed"
			case "response.incomplete":
				snapshot.Status = "incomplete"
			case "response.cancelled":
				snapshot.Status = "cancelled"
			}
		}

		switch event.name {
		case "response.output_text.delta":
			var delta struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(event.data, &delta); err != nil {
				return fmt.Errorf("decode Responses text delta: %w", err)
			}
			if delta.Delta != "" {
				if !printed {
					if _, err := fmt.Fprintf(writer, "[%s] ", agentName); err != nil {
						return err
					}
					printed = true
				}
				if _, err := io.WriteString(writer, delta.Delta); err != nil {
					return err
				}
			}
		case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
			terminal = true
			if printed {
				if _, err := fmt.Fprintln(writer); err != nil {
					return err
				}
			} else if err := writeResponsesSnapshot(writer, agentName, snapshot, envelope.Response); err != nil {
				return err
			}
		case "error":
			var streamErr responsesError
			if err := json.Unmarshal(event.data, &streamErr); err != nil {
				return fmt.Errorf("decode Responses stream error: %w", err)
			}
			return fmt.Errorf("agent error (%s): %s", streamErr.Code, streamErr.Message)
		}

		if envelope.SequenceNumber != nil {
			cursor = new(*envelope.SequenceNumber)
		}
		if snapshot.Status != "" {
			status = snapshot.Status
		}
		if isTerminalResponseStatus(status) {
			terminal = true
		}
		if onProgress != nil {
			if err := onProgress(responsesStreamProgress{
				ResponseID: identity,
				Cursor:     cursor,
				Status:     status,
				EventType:  event.name,
				Terminal:   terminal,
			}); err != nil {
				return err
			}
		}
		if snapshot.Status == "failed" {
			if snapshot.Error != nil {
				return fmt.Errorf("agent failed (%s): %s", snapshot.Error.Code, snapshot.Error.Message)
			}
			return fmt.Errorf("agent returned failed status")
		}
		if snapshot.Status == "incomplete" && requireTerminal {
			return fmt.Errorf("agent returned incomplete status")
		}
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			addedBytes := len(value)
			if dataSeen {
				addedBytes++
			}
			if addedBytes > maxEventBytes-eventData.Len() {
				return fmt.Errorf("Responses SSE event exceeds %d bytes", maxEventBytes)
			}
			if dataSeen {
				eventData.WriteByte('\n')
			}
			eventData.WriteString(value)
			dataSeen = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response stream: %w", err)
	}
	if err := dispatch(); err != nil {
		return err
	}
	if requireTerminal && !terminal {
		if identity == "" {
			return fmt.Errorf("background Response stream ended before its identity was received")
		}
		return fmt.Errorf("background Response %s disconnected before reaching a terminal state", identity)
	}
	return nil
}

func writeResponsesSnapshot(
	writer io.Writer,
	agentName string,
	snapshot responsesSnapshot,
	raw json.RawMessage,
) error {
	var printed bool
	for _, item := range snapshot.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" {
				if _, err := fmt.Fprintf(writer, "[%s] %s\n", agentName, content.Text); err != nil {
					return err
				}
				printed = true
			}
		}
	}
	if printed || len(raw) == 0 {
		return nil
	}

	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		_, err = fmt.Fprintln(writer, string(raw))
		return err
	}
	_, err := fmt.Fprintln(writer, formatted.String())
	return err
}

func isKnownResponsesEvent(event string) bool {
	switch event {
	case "response.created", "response.queued", "response.in_progress", "response.output_text.delta",
		"response.completed", "response.failed", "response.incomplete", "response.cancelled", "error":
		return true
	default:
		return false
	}
}

func readSSEStream(body io.Reader, agentName string) error {
	return readResponsesSSE(context.Background(), body, os.Stdout, agentName, false, nil)
}
