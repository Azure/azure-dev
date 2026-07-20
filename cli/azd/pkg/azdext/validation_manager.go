// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// validationCheckKey uniquely identifies a registered validation check.
type validationCheckKey struct {
	CheckType string
	RuleID    string
}

// contextAssembler reassembles chunked context data received via
// PrepareValidationContextChunk messages.
// chunkEntry stores a received chunk for ordered reassembly.
type chunkEntry struct {
	index int32
	data  []byte
}

type contextAssembler struct {
	checkType      string
	chunks         map[string][]chunkEntry // key → received chunks (may be out of order)
	expectedChunks map[string]int          // key → expected chunk count (set when IsLastChunk seen)
	totalKeys      int                     // expected number of distinct keys
	// done is closed exactly once when assembly completes. The IsLastKey
	// chunk handler waits on it so it can deterministically own the ack the
	// core's SendAndWait is keyed on, even if a different chunk handler is the
	// one that finishes assembly.
	done chan struct{}
}

// addChunk appends a chunk to the assembler. Returns the completed
// context map when all keys have all their chunks and the expected
// total_keys count is met.
//
// Because the broker dispatches messages to goroutines concurrently,
// chunks may arrive out of order. The assembler stores chunks by
// index and reassembles them in order upon completion.
func (a *contextAssembler) addChunk(
	chunk *PrepareValidationContextChunk,
) (complete bool, result map[string][]byte) {
	if a.chunks == nil {
		a.chunks = make(map[string][]chunkEntry)
		a.expectedChunks = make(map[string]int)
	}

	if chunk.TotalKeys > 0 {
		a.totalKeys = int(chunk.TotalKeys)
	}

	a.chunks[chunk.Key] = append(a.chunks[chunk.Key], chunkEntry{
		index: chunk.ChunkIndex,
		data:  chunk.Data,
	})

	if chunk.IsLastChunk {
		// ChunkIndex is 0-based, so total chunks = index + 1
		a.expectedChunks[chunk.Key] = int(chunk.ChunkIndex) + 1
	}

	// Complete when:
	// 1. We know the total number of keys
	// 2. We've seen the last chunk for every key (so we know expected counts)
	// 3. We have all expected chunks for every key
	if a.totalKeys > 0 && len(a.expectedChunks) == a.totalKeys {
		allPresent := true
		for key, expected := range a.expectedChunks {
			if len(a.chunks[key]) != expected {
				allPresent = false
				break
			}
		}
		if allPresent {
			assembled := make(map[string][]byte, len(a.chunks))
			for key, entries := range a.chunks {
				slices.SortFunc(entries, func(a, b chunkEntry) int {
					return cmp.Compare(a.index, b.index)
				})
				var buf bytes.Buffer
				for _, e := range entries {
					buf.Write(e.data)
				}
				assembled[key] = buf.Bytes()
			}
			return true, assembled
		}
	}
	return false, nil
}

// ValidationManager manages validation check providers on the extension side.
// It handles registration with the core and dispatching incoming check requests
// to the appropriate provider.
type ValidationManager struct {
	extensionId  string
	client       *AzdClient
	broker       *grpcbroker.MessageBroker[ValidationMessage]
	brokerLogger *log.Logger

	// factories maps check keys to their factory functions.
	factories map[validationCheckKey]ValidationCheckProviderFactory
	// instances maps check keys to instantiated providers.
	instances map[validationCheckKey]ValidationCheckProvider
	// cachedContexts maps context_id to assembled context data.
	cachedContexts map[string]*ValidationContext
	// contextRefCounts tracks the number of pending checks for each context_id.
	// When the count reaches zero, the context is evicted.
	contextRefCounts map[string]int
	// assemblers maps context_id to in-progress chunk assemblers.
	assemblers map[string]*contextAssembler

	mu sync.RWMutex
}

// NewValidationManager creates a new ValidationManager.
func NewValidationManager(
	extensionId string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *ValidationManager {
	return &ValidationManager{
		extensionId:      extensionId,
		client:           client,
		brokerLogger:     brokerLogger,
		factories:        make(map[validationCheckKey]ValidationCheckProviderFactory),
		instances:        make(map[validationCheckKey]ValidationCheckProvider),
		cachedContexts:   make(map[string]*ValidationContext),
		contextRefCounts: make(map[string]int),
		assemblers:       make(map[string]*contextAssembler),
	}
}

// Close terminates the underlying gRPC stream.
func (m *ValidationManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}

	clear(m.factories)
	clear(m.instances)
	clear(m.cachedContexts)
	clear(m.contextRefCounts)
	clear(m.assemblers)

	return nil
}

// ensureStream initializes the broker and stream if not yet created.
func (m *ValidationManager) ensureStream(ctx context.Context) error {
	m.mu.RLock()
	if m.broker != nil {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		return nil
	}

	stream, err := m.client.Validation().Stream(ctx)
	if err != nil {
		return fmt.Errorf(
			"failed to create validation stream: %w", err,
		)
	}

	envelope := NewValidationEnvelope()
	m.broker = grpcbroker.NewMessageBroker(
		stream, envelope, m.extensionId, m.brokerLogger,
	)

	if err := m.registerHandlers(); err != nil {
		m.broker.Close()
		m.broker = nil
		return fmt.Errorf(
			"failed to register validation handlers: %w", err,
		)
	}

	return nil
}

// registerHandlers registers message handlers with the broker.
func (m *ValidationManager) registerHandlers() error {
	if err := m.broker.On(m.onPrepareContextChunk); err != nil {
		return err
	}
	return m.broker.On(m.onValidationCheck)
}

// Register registers a validation check with the core, waits for the
// response, then starts handling incoming check requests.
func (m *ValidationManager) Register(
	ctx context.Context,
	factory ValidationCheckProviderFactory,
	checkType string,
	ruleID string,
) error {
	if strings.TrimSpace(checkType) == "" {
		return fmt.Errorf("validation check type cannot be empty")
	}
	if strings.TrimSpace(ruleID) == "" {
		return fmt.Errorf("validation check rule ID cannot be empty")
	}

	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf(
			"failed to ensure validation stream: %w", err,
		)
	}

	key := validationCheckKey{
		CheckType: checkType, RuleID: ruleID,
	}

	m.mu.Lock()
	if _, exists := m.factories[key]; exists {
		m.mu.Unlock()
		return fmt.Errorf(
			"validation check '%s/%s' already registered",
			checkType, ruleID,
		)
	}
	m.factories[key] = factory
	m.mu.Unlock()

	registerReq := &ValidationMessage{
		RequestId: uuid.NewString(),
		MessageType: &ValidationMessage_RegisterValidationCheckRequest{
			RegisterValidationCheckRequest: &RegisterValidationCheckRequest{
				CheckType: checkType,
				RuleId:    ruleID,
			},
		},
	}

	resp, err := m.broker.SendAndWait(ctx, registerReq)
	if err != nil {
		m.mu.Lock()
		delete(m.factories, key)
		m.mu.Unlock()
		return fmt.Errorf(
			"validation check registration failed: %w", err,
		)
	}

	if resp == nil {
		m.mu.Lock()
		delete(m.factories, key)
		m.mu.Unlock()
		return fmt.Errorf(
			"validation check registration: received nil response",
		)
	}

	if resp.GetRegisterValidationCheckResponse() == nil {
		m.mu.Lock()
		delete(m.factories, key)
		m.mu.Unlock()
		return fmt.Errorf(
			"expected RegisterValidationCheckResponse, got %T",
			resp.GetMessageType(),
		)
	}

	return nil
}

// Receive starts the broker dispatcher, blocking until stream ends.
func (m *ValidationManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf(
			"failed to ensure validation stream for receiving: %w",
			err,
		)
	}

	return m.broker.Run(ctx)
}

// Ready blocks until the message broker is ready.
func (m *ValidationManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf(
			"failed to ensure validation stream for readiness: %w",
			err,
		)
	}

	return m.broker.Ready(ctx)
}

// getOrCreateProvider retrieves or creates a provider instance.
func (m *ValidationManager) getOrCreateProvider(
	key validationCheckKey,
) (ValidationCheckProvider, error) {
	m.mu.RLock()
	if instance, exists := m.instances[key]; exists {
		m.mu.RUnlock()
		return instance, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if instance, exists := m.instances[key]; exists {
		return instance, nil
	}

	factory, exists := m.factories[key]
	if !exists {
		return nil, fmt.Errorf(
			"no factory for validation check '%s/%s'",
			key.CheckType, key.RuleID,
		)
	}

	provider := factory()
	m.instances[key] = provider
	return provider, nil
}

// onPrepareContextChunk handles incoming context chunks from the core.
// It reassembles chunks and caches the completed context.
func (m *ValidationManager) onPrepareContextChunk(
	ctx context.Context,
	chunk *PrepareValidationContextChunk,
) (*ValidationMessage, error) {
	contextID := chunk.GetContextId()

	m.mu.Lock()
	assembler, exists := m.assemblers[contextID]
	if !exists {
		assembler = &contextAssembler{
			checkType: chunk.GetCheckType(),
			done:      make(chan struct{}),
		}
		m.assemblers[contextID] = assembler
	}
	done := assembler.done

	complete, data := assembler.addChunk(chunk)
	if complete {
		m.cachedContexts[contextID] = &ValidationContext{
			ContextID: contextID,
			CheckType: chunk.GetCheckType(),
			Data:      data,
		}
		delete(m.assemblers, contextID)
		// Signal any IsLastKey handler that is waiting for assembly to finish.
		close(done)
	}
	m.mu.Unlock()

	// Only the IsLastKey chunk carries the request_id the core's SendAndWait
	// blocks on; every other chunk is fire-and-forget on the core side. To
	// avoid sending an ack with the wrong request_id (which would leave the
	// core blocked until cancellation), non-IsLastKey handlers never ack —
	// even if they happen to be the handler that completes assembly.
	if !chunk.GetIsLastKey() {
		return nil, nil
	}

	// Chunks are dispatched to concurrent goroutines, so the IsLastKey chunk's
	// handler may run before earlier chunks finish assembling. Wait for the
	// completing handler to signal done before sending the ack, while
	// respecting cancellation so a missing chunk can't leak this goroutine.
	if !complete {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &ValidationMessage{
		MessageType: &ValidationMessage_PrepareValidationContextResponse{
			PrepareValidationContextResponse: &PrepareValidationContextResponse{},
		},
	}, nil
}

// onValidationCheck handles incoming check requests from the core.
func (m *ValidationManager) onValidationCheck(
	ctx context.Context,
	req *ValidationCheckRequest,
) (*ValidationMessage, error) {
	key := validationCheckKey{
		CheckType: req.GetCheckType(),
		RuleID:    req.GetRuleId(),
	}

	provider, err := m.getOrCreateProvider(key)
	if err != nil {
		return nil, fmt.Errorf(
			"getting validation check provider: %w", err,
		)
	}

	// Look up cached context and increment ref count
	m.mu.Lock()
	valCtx := m.cachedContexts[req.GetContextId()]
	hasRef := valCtx != nil
	if hasRef {
		m.contextRefCounts[req.GetContextId()]++
	}
	m.mu.Unlock()

	// Ensure ref count is decremented on all exit paths (success and error)
	if hasRef {
		defer func() {
			m.mu.Lock()
			contextID := req.GetContextId()
			m.contextRefCounts[contextID]--
			if m.contextRefCounts[contextID] <= 0 {
				delete(m.cachedContexts, contextID)
				delete(m.contextRefCounts, contextID)
			}
			m.mu.Unlock()
		}()
	}

	if valCtx == nil {
		valCtx = &ValidationContext{
			ContextID: req.GetContextId(),
			CheckType: req.GetCheckType(),
			Data:      make(map[string][]byte),
		}
	}

	resp, err := provider.Validate(ctx, valCtx, req)
	if err != nil {
		return nil, fmt.Errorf(
			"validation check '%s/%s' failed: %w",
			key.CheckType, key.RuleID, err,
		)
	}

	// Normalize nil response to empty to avoid nil pointer in oneof wrapper
	if resp == nil {
		resp = &ValidationCheckResponse{}
	}

	return &ValidationMessage{
		MessageType: &ValidationMessage_ValidationCheckResponse{
			ValidationCheckResponse: resp,
		},
	}, nil
}
