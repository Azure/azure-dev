// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// readSSEStream reads a Server-Sent Events stream from the Foundry Responses API,
// printing text deltas in real-time and returning the final response or any error.
func readSSEStream(body io.Reader, agentName string) error {
	scanner := bufio.NewScanner(body)
	// Allow large SSE data lines (up to 1 MB)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentEvent string
	var printed bool
	var printedLineClosed bool
	var finalErr error
	var terminalSeen bool

	closePrintedLine := func() {
		if printed && !printedLineClosed {
			fmt.Println()
			printedLineClosed = true
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if terminalSeen {
			continue
		}

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			currentEvent = after
			continue
		}

		if data, ok := strings.CutPrefix(line, "data: "); ok {
			switch currentEvent {
			case "response.output_text.delta":
				var delta struct {
					Delta string `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &delta); err == nil && delta.Delta != "" {
					if !printed {
						fmt.Printf("[%s] ", agentName)
						printed = true
					}
					printedLineClosed = false
					fmt.Print(delta.Delta)
				}

			case "response.completed":
				terminalSeen = true
				closePrintedLine()
				// Parse the completed response to check for errors
				var event struct {
					Response json.RawMessage `json:"response"`
				}
				if err := json.Unmarshal([]byte(data), &event); err == nil && event.Response != nil {
					var result map[string]any
					if err := json.Unmarshal(event.Response, &result); err == nil {
						if status, _ := result["status"].(string); status == "failed" {
							if errObj, ok := result["error"].(map[string]any); ok {
								msg, _ := errObj["message"].(string)
								code, _ := errObj["code"].(string)
								if finalErr == nil {
									finalErr = fmt.Errorf("agent failed (%s): %s", code, msg)
								}
								break
							}
							if finalErr == nil {
								finalErr = fmt.Errorf("agent returned failed status")
							}
							break
						}
						// If no text was streamed, extract output from the completed response
						if !printed && finalErr == nil {
							finalErr = printAgentResponse(result, agentName)
						}
					}
				}

			case "error":
				terminalSeen = true
				closePrintedLine()
				var sseErr struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &sseErr); err == nil {
					if finalErr == nil {
						finalErr = fmt.Errorf("agent error (%s): %s", sseErr.Code, sseErr.Message)
					}
					break
				}
				if finalErr == nil {
					finalErr = fmt.Errorf("agent stream error: %s", data)
				}
			}

			currentEvent = ""
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading response stream: %w", err)
	}

	if finalErr != nil {
		return finalErr
	}
	closePrintedLine()
	return nil
}

// printAgentResponse pretty-prints the output_text items from an agent response.
func printAgentResponse(result map[string]any, title string) error {
	// Check for agent-level errors (e.g., agent runtime failures)
	if status, _ := result["status"].(string); status == "failed" {
		if errObj, ok := result["error"].(map[string]any); ok {
			msg, _ := errObj["message"].(string)
			code, _ := errObj["code"].(string)
			return fmt.Errorf("agent failed (%s): %s", code, msg)
		}
		return fmt.Errorf("agent returned failed status")
	}

	// Check for server-level errors (e.g., local agentserver: {"code": "server_error", "message": "..."})
	if code, ok := result["code"].(string); ok && code != "" {
		msg, _ := result["message"].(string)
		return fmt.Errorf("agent error (%s): %s", code, msg)
	}

	outputItems, ok := result["output"].([]any)
	if !ok {
		// Try printing the whole response as formatted JSON
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
		return nil
	}

	printed := false
	for _, item := range outputItems {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		contentItems, ok := itemMap["content"].([]any)
		if !ok {
			continue
		}
		for _, content := range contentItems {
			contentMap, ok := content.(map[string]any)
			if !ok {
				continue
			}
			if contentMap["type"] == "output_text" {
				if text, ok := contentMap["text"].(string); ok {
					fmt.Printf("[%s] %s\n", title, text)
					printed = true
				}
			}
		}
	}

	if !printed {
		jsonBytes, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(jsonBytes))
	}
	return nil
}
