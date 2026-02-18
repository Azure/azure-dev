// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// PromptType represents the type of prompt to display
type PromptType string

const (
	PromptTypeString      PromptType = "string"
	PromptTypePassword    PromptType = "password"
	PromptTypeDirectory   PromptType = "directory"
	PromptTypeSelect      PromptType = "select"
	PromptTypeMultiSelect PromptType = "multiSelect"
	PromptTypeConfirm     PromptType = "confirm"
)

// PromptChoice represents a choice option for select/multiselect prompts
type PromptChoice struct {
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
}

// PromptOptions contains the options for a prompt
type PromptOptions struct {
	Message      string         `json:"message"`
	Help         string         `json:"help,omitempty"`
	Choices      []PromptChoice `json:"choices,omitempty"`
	DefaultValue any            `json:"defaultValue,omitempty"`
}

// PromptRequest represents an incoming prompt request from azd
type PromptRequest struct {
	Type    PromptType    `json:"type"`
	Options PromptOptions `json:"options"`
}

// PromptDialogRequest represents a dialog-style prompt request (multiple prompts at once)
type PromptDialogRequest struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Prompts     []PromptDialogPrompt `json:"prompts"`
}

// PromptDialogPrompt represents a single prompt within a dialog
type PromptDialogPrompt struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"` // "string", "select", etc.
	DisplayName  string               `json:"displayName"`
	Description  *string              `json:"description,omitempty"`
	DefaultValue *string              `json:"defaultValue,omitempty"`
	Required     bool                 `json:"required"`
	Choices      []PromptDialogChoice `json:"choices,omitempty"`
}

// PromptDialogChoice represents a choice in a dialog prompt
type PromptDialogChoice struct {
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
}

// PromptDialogResponse represents the response to a dialog request
type PromptDialogResponse struct {
	Result  string                      `json:"result"` // "success", "cancelled", "error"
	Message *string                     `json:"message,omitempty"`
	Inputs  []PromptDialogResponseInput `json:"inputs,omitempty"`
}

// PromptDialogResponseInput represents a single input value in a dialog response
type PromptDialogResponseInput struct {
	ID    string `json:"id"`
	Value any    `json:"value"`
}

// PromptResponseStatus represents the status of a prompt response
type PromptResponseStatus string

const (
	PromptStatusSuccess   PromptResponseStatus = "success"
	PromptStatusCancelled PromptResponseStatus = "cancelled"
	PromptStatusError     PromptResponseStatus = "error"
)

// PromptResponse represents the response to send back to azd
type PromptResponse struct {
	Status  PromptResponseStatus `json:"status"`
	Value   any                  `json:"value,omitempty"`
	Message string               `json:"message,omitempty"`
}

// PromptServer handles external prompting requests from azd subprocesses
type PromptServer struct {
	listener   net.Listener
	server     *http.Server
	endpoint   string
	key        string
	ui         *tea.Program
	mu         sync.Mutex
	pendingReq *pendingPrompt
	ctx        context.Context
	cancel     context.CancelFunc
	debugLog   *log.Logger
}

// pendingPrompt tracks an active prompt waiting for user response
type pendingPrompt struct {
	request  *PromptRequest
	response chan *PromptResponse
}

// pendingDialogPrompt tracks an active dialog prompt waiting for user response
type pendingDialogPrompt struct {
	request  *PromptDialogRequest
	response chan *PromptDialogResponse
}

// promptRequestMsg is sent to the TUI when a prompt arrives
type promptRequestMsg struct {
	request *PromptRequest
}

// promptDialogRequestMsg is sent to the TUI when a dialog prompt arrives
type promptDialogRequestMsg struct {
	request *PromptDialogRequest
}

// promptResponseMsg is sent from the TUI when the user responds
type promptResponseMsg struct {
	response *PromptResponse
}

// NewPromptServer creates a new prompt server
func NewPromptServer(ctx context.Context) (*PromptServer, error) {
	// Create debug log file
	logFile, err := os.OpenFile("/tmp/concurx-prompt-server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logFile = os.Stderr // fallback to stderr
	}
	debugLog := log.New(logFile, "[PROMPT-SERVER] ", log.LstdFlags|log.Lmicroseconds)

	// Generate random key for authentication
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate prompt key: %w", err)
	}
	key := hex.EncodeToString(keyBytes)

	// Create listener on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	endpoint := fmt.Sprintf("http://%s", listener.Addr().String())

	debugLog.Printf("Created prompt server: endpoint=%s", endpoint)

	serverCtx, cancel := context.WithCancel(ctx)

	ps := &PromptServer{
		listener: listener,
		endpoint: endpoint,
		key:      key,
		ctx:      serverCtx,
		cancel:   cancel,
		debugLog: debugLog,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/prompt", ps.handlePrompt)

	ps.server = &http.Server{
		Handler: mux,
	}

	return ps, nil
}

// Start begins serving prompt requests
func (ps *PromptServer) Start() {
	ps.debugLog.Printf("Starting prompt server at %s", ps.endpoint)
	go func() {
		err := ps.server.Serve(ps.listener)
		if err != nil && err != http.ErrServerClosed {
			ps.debugLog.Printf("Server error: %v", err)
		}
	}()
}

// Stop shuts down the prompt server
func (ps *PromptServer) Stop() {
	ps.cancel()
	_ = ps.server.Shutdown(context.Background())
}

// SetUI sets the Bubble Tea program to send prompt requests to
func (ps *PromptServer) SetUI(ui *tea.Program) {
	ps.ui = ui
}

// Endpoint returns the server endpoint URL
func (ps *PromptServer) Endpoint() string {
	return ps.endpoint
}

// Key returns the authentication key
func (ps *PromptServer) Key() string {
	return ps.key
}

// EnvVars returns the environment variables to set for azd subprocesses
func (ps *PromptServer) EnvVars() []string {
	return []string{
		fmt.Sprintf("AZD_UI_PROMPT_ENDPOINT=%s", ps.endpoint),
		fmt.Sprintf("AZD_UI_PROMPT_KEY=%s", ps.key),
		// Disable prompt dialog to get individual prompts with full options (e.g., location list)
		"AZD_UI_NO_PROMPT_DIALOG=1",
	}
}

// RespondToPrompt sends a response back to the waiting prompt request
func (ps *PromptServer) RespondToPrompt(response *PromptResponse) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.pendingReq != nil && ps.pendingReq.response != nil {
		ps.pendingReq.response <- response
		ps.pendingReq = nil
	}
}

// handlePrompt handles incoming prompt requests from azd
func (ps *PromptServer) handlePrompt(w http.ResponseWriter, r *http.Request) {
	ps.debugLog.Printf("Received request: %s %s", r.Method, r.URL.String())

	// Only accept POST requests
	if r.Method != http.MethodPost {
		ps.debugLog.Printf("Method not allowed: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate authorization header
	authHeader := r.Header.Get("Authorization")
	expectedAuth := fmt.Sprintf("Bearer %s", ps.key)
	if authHeader != expectedAuth {
		ps.debugLog.Printf("Unauthorized: got '%s', expected '%s'", authHeader, expectedAuth)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Read and log raw body for debugging
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		ps.debugLog.Printf("Failed to read body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	ps.debugLog.Printf("Raw body: %s", string(bodyBytes))

	// Try to detect request format by checking for "prompts" field (dialog) vs "type" field (simple)
	var rawMsg map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &rawMsg); err != nil {
		ps.debugLog.Printf("Bad request (raw parse): %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Check if this is a dialog request (has "prompts" field)
	if _, hasPrompts := rawMsg["prompts"]; hasPrompts {
		ps.debugLog.Printf("Detected DIALOG request format")
		ps.handleDialogPrompt(w, bodyBytes)
		return
	}

	// Otherwise it's a simple prompt request
	ps.debugLog.Printf("Detected SIMPLE prompt request format")
	ps.handleSimplePrompt(w, bodyBytes)
}

// handleSimplePrompt handles the simple single-prompt format
func (ps *PromptServer) handleSimplePrompt(w http.ResponseWriter, bodyBytes []byte) {
	// Parse request body
	var req PromptRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		ps.debugLog.Printf("Bad request: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ps.debugLog.Printf("Parsed prompt request: type=%s, message=%s, choices=%d", req.Type, req.Options.Message, len(req.Options.Choices))

	// Create response channel
	responseChan := make(chan *PromptResponse, 1)

	// Store pending request
	ps.mu.Lock()
	ps.pendingReq = &pendingPrompt{
		request:  &req,
		response: responseChan,
	}
	ps.mu.Unlock()

	// Send prompt request to TUI
	if ps.ui != nil {
		ps.debugLog.Printf("Sending prompt request to TUI")
		ps.ui.Send(promptRequestMsg{request: &req})
	} else {
		ps.debugLog.Printf("WARNING: ui is nil, cannot send prompt request")
	}

	ps.debugLog.Printf("Waiting for response...")

	// Wait for response or context cancellation
	var response *PromptResponse
	select {
	case response = <-responseChan:
		ps.debugLog.Printf("Got response from user: status=%s", response.Status)
	case <-ps.ctx.Done():
		ps.debugLog.Printf("Context cancelled, server shutting down")
		response = &PromptResponse{
			Status:  PromptStatusCancelled,
			Message: "Server shutting down",
		}
	}

	// Send response
	ps.debugLog.Printf("Sending response: %+v", response)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// handleDialogPrompt handles the dialog-style multi-prompt format
func (ps *PromptServer) handleDialogPrompt(w http.ResponseWriter, bodyBytes []byte) {
	// Parse dialog request
	var dialogReq PromptDialogRequest
	if err := json.Unmarshal(bodyBytes, &dialogReq); err != nil {
		ps.debugLog.Printf("Bad dialog request: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ps.debugLog.Printf("Parsed dialog request: title=%s, description=%s, prompts=%d",
		dialogReq.Title, dialogReq.Description, len(dialogReq.Prompts))

	// For now, we'll process each prompt in the dialog sequentially
	// by converting them to simple prompts
	inputs := make([]PromptDialogResponseInput, 0, len(dialogReq.Prompts))

	for _, prompt := range dialogReq.Prompts {
		ps.debugLog.Printf("Processing dialog prompt: id=%s, kind=%s, displayName=%s",
			prompt.ID, prompt.Kind, prompt.DisplayName)

		// Convert dialog prompt to simple prompt
		simpleReq := ps.convertDialogPromptToSimple(&prompt)

		// Create response channel
		responseChan := make(chan *PromptResponse, 1)

		// Store pending request
		ps.mu.Lock()
		ps.pendingReq = &pendingPrompt{
			request:  simpleReq,
			response: responseChan,
		}
		ps.mu.Unlock()

		// Send prompt request to TUI
		if ps.ui != nil {
			ps.debugLog.Printf("Sending dialog prompt to TUI: %s", prompt.ID)
			ps.ui.Send(promptRequestMsg{request: simpleReq})
		} else {
			ps.debugLog.Printf("WARNING: ui is nil, cannot send prompt request")
		}

		// Wait for response
		var response *PromptResponse
		select {
		case response = <-responseChan:
			ps.debugLog.Printf("Got response for prompt %s: status=%s", prompt.ID, response.Status)
		case <-ps.ctx.Done():
			ps.debugLog.Printf("Context cancelled during dialog")
			dialogResp := &PromptDialogResponse{
				Result:  "cancelled",
				Message: stringPtr("Server shutting down"),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dialogResp)
			return
		}

		// Check for cancellation
		if response.Status == PromptStatusCancelled {
			dialogResp := &PromptDialogResponse{
				Result: "cancelled",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dialogResp)
			return
		}

		// Check for error
		if response.Status == PromptStatusError {
			dialogResp := &PromptDialogResponse{
				Result:  "error",
				Message: stringPtr(response.Message),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(dialogResp)
			return
		}

		// Add to inputs
		inputs = append(inputs, PromptDialogResponseInput{
			ID:    prompt.ID,
			Value: response.Value,
		})
	}

	// Send success response with all inputs
	dialogResp := &PromptDialogResponse{
		Result: "success",
		Inputs: inputs,
	}
	ps.debugLog.Printf("Sending dialog response: %+v", dialogResp)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dialogResp)
}

// convertDialogPromptToSimple converts a dialog prompt to a simple prompt request
func (ps *PromptServer) convertDialogPromptToSimple(prompt *PromptDialogPrompt) *PromptRequest {
	var promptType PromptType
	switch prompt.Kind {
	case "string":
		promptType = PromptTypeString
	case "password":
		promptType = PromptTypePassword
	case "select":
		promptType = PromptTypeSelect
	case "multiSelect":
		promptType = PromptTypeMultiSelect
	case "confirm":
		promptType = PromptTypeConfirm
	default:
		promptType = PromptTypeString
	}

	// Convert choices
	var choices []PromptChoice
	if len(prompt.Choices) > 0 {
		choices = make([]PromptChoice, len(prompt.Choices))
		for i, c := range prompt.Choices {
			detail := ""
			if c.Description != nil {
				detail = *c.Description
			}
			choices[i] = PromptChoice{
				Value:  c.Value,
				Detail: detail,
			}
		}
	}

	// Build message
	message := prompt.DisplayName
	if prompt.Description != nil && *prompt.Description != "" {
		message = fmt.Sprintf("%s\n%s", prompt.DisplayName, *prompt.Description)
	}

	// Get default value
	var defaultValue any
	if prompt.DefaultValue != nil {
		defaultValue = *prompt.DefaultValue
	}

	return &PromptRequest{
		Type: promptType,
		Options: PromptOptions{
			Message:      message,
			Choices:      choices,
			DefaultValue: defaultValue,
		},
	}
}

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
	return &s
}
