// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validationContextChunkSize is the maximum size of each context data chunk
// sent via PrepareValidationContextChunk. Kept well under gRPC's default
// 4 MB max message size to leave room for protobuf framing.
const validationContextChunkSize = 2 * 1024 * 1024 // 2 MB

// validationCheckEntry tracks a registered validation check from an extension.
type validationCheckEntry struct {
	CheckType string
	RuleID    string
	Extension *extensions.Extension
	Broker    *grpcbroker.MessageBroker[azdext.ValidationMessage]
}

// ValidationService implements azdext.ValidationServiceServer.
type ValidationService struct {
	azdext.UnimplementedValidationServiceServer
	extensionManager *extensions.Manager

	checks   []validationCheckEntry
	checksMu sync.RWMutex
}

// NewValidationService creates a new ValidationService instance.
func NewValidationService(
	extensionManager *extensions.Manager,
) *ValidationService {
	return &ValidationService{
		extensionManager: extensionManager,
	}
}

// Stream handles the bidirectional streaming for validation operations.
func (s *ValidationService) Stream(
	stream azdext.ValidationService_StreamServer,
) error {
	ctx := stream.Context()
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf(
			"failed to get extension claims: %w", err,
		)
	}

	options := extensions.FilterOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return status.Errorf(
			codes.FailedPrecondition,
			"failed to get extension: %s", err.Error(),
		)
	}

	if !extension.HasCapability(
		extensions.ValidationProviderCapability,
	) {
		return status.Errorf(
			codes.PermissionDenied,
			"extension does not support validation-provider capability",
		)
	}

	log.Printf("validation stream: extension %s connected", extension.Id)

	// Create message broker for this stream
	ops := azdext.NewValidationEnvelope()
	broker := grpcbroker.NewMessageBroker(
		stream, ops, extension.Id, log.Default(),
	)

	// Track registered checks for cleanup
	var registeredKeys []validationCheckEntry
	var registeredMu sync.Mutex

	// Register handler for registration requests
	err = broker.On(func(
		ctx context.Context,
		req *azdext.RegisterValidationCheckRequest,
	) (*azdext.ValidationMessage, error) {
		return s.onRegisterRequest(
			ctx, req, extension, broker,
			&registeredKeys, &registeredMu,
		)
	})

	if err != nil {
		return fmt.Errorf(
			"failed to register validation handler: %w", err,
		)
	}

	// Run the broker dispatcher (blocking)
	if err := broker.Run(ctx); err != nil &&
		!errors.Is(err, context.Canceled) {
		registeredMu.Lock()
		keys := slices.Clone(registeredKeys)
		registeredMu.Unlock()
		log.Printf(
			"Validation broker error for %d checks: %v",
			len(keys), err,
		)
		return fmt.Errorf("broker error: %w", err)
	}

	// Clean up registered checks when stream closes
	s.checksMu.Lock()
	s.checks = slices.DeleteFunc(s.checks, func(e validationCheckEntry) bool {
		for _, registered := range registeredKeys {
			if e.CheckType == registered.CheckType &&
				e.RuleID == registered.RuleID &&
				e.Extension.Id == registered.Extension.Id {
				return true
			}
		}
		return false
	})
	s.checksMu.Unlock()

	return nil
}

// onRegisterRequest handles registration of a validation check.
func (s *ValidationService) onRegisterRequest(
	_ context.Context,
	req *azdext.RegisterValidationCheckRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ValidationMessage],
	registeredKeys *[]validationCheckEntry,
	registeredMu *sync.Mutex,
) (*azdext.ValidationMessage, error) {
	checkType := req.GetCheckType()
	ruleID := req.GetRuleId()

	if strings.TrimSpace(checkType) == "" {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"check_type cannot be empty",
		)
	}
	if strings.TrimSpace(ruleID) == "" {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"rule_id cannot be empty",
		)
	}

	entry := validationCheckEntry{
		CheckType: checkType,
		RuleID:    ruleID,
		Extension: extension,
		Broker:    broker,
	}

	s.checksMu.Lock()
	// Reject duplicate registrations (same extension + check_type + rule_id)
	for _, existing := range s.checks {
		if existing.CheckType == checkType &&
			existing.RuleID == ruleID &&
			existing.Extension.Id == extension.Id {
			s.checksMu.Unlock()
			return nil, status.Errorf(
				codes.AlreadyExists,
				"validation check '%s/%s' already registered by extension %s",
				checkType, ruleID, extension.Id,
			)
		}
	}
	s.checks = append(s.checks, entry)
	s.checksMu.Unlock()

	registeredMu.Lock()
	*registeredKeys = append(*registeredKeys, entry)
	registeredMu.Unlock()

	log.Printf(
		"registered validation check: %s/%s from %s",
		checkType, ruleID, extension.Id,
	)

	return &azdext.ValidationMessage{
		MessageType: &azdext.ValidationMessage_RegisterValidationCheckResponse{
			RegisterValidationCheckResponse: &azdext.RegisterValidationCheckResponse{},
		},
	}, nil
}

// DispatchChecks invokes all registered checks for the given checkType.
// It groups checks by extension, sends context once per extension via
// chunked delivery, then dispatches check requests in parallel.
func (s *ValidationService) DispatchChecks(
	ctx context.Context,
	checkType string,
	contextData map[string][]byte,
) ([]*azdext.ValidationCheckResult, []string, error) {
	s.checksMu.RLock()
	matching := make([]validationCheckEntry, 0)
	for _, entry := range s.checks {
		if entry.CheckType == checkType {
			matching = append(matching, entry)
		}
	}
	s.checksMu.RUnlock()

	if len(matching) == 0 {
		return nil, nil, nil
	}

	// Group checks by broker (extension). Each extension shares one broker.
	type brokerGroup struct {
		broker  *grpcbroker.MessageBroker[azdext.ValidationMessage]
		extID   string
		entries []validationCheckEntry
	}
	groups := make(map[*grpcbroker.MessageBroker[azdext.ValidationMessage]]*brokerGroup)
	for _, e := range matching {
		g, exists := groups[e.Broker]
		if !exists {
			g = &brokerGroup{broker: e.Broker, extID: e.Extension.Id}
			groups[e.Broker] = g
		}
		g.entries = append(g.entries, e)
	}

	var (
		allResults     []*azdext.ValidationCheckResult
		invokedRuleIDs []string
		mu             sync.Mutex
		wg             sync.WaitGroup
		errs           []error
	)

	for _, group := range groups {
		g := group
		contextID := uuid.NewString()

		log.Printf(
			"dispatching %d validation checks to extension %s (context_id=%s)",
			len(g.entries), g.extID, contextID,
		)

		// Phase 1: Send context chunks to this extension
		if err := sendContextChunks(
			ctx, g.broker, contextID, checkType, contextData,
		); err != nil {
			log.Printf(
				"failed to send context to extension %s: %v",
				g.extID, err,
			)
			mu.Lock()
			errs = append(errs, fmt.Errorf(
				"context delivery to %s: %w", g.extID, err,
			))
			mu.Unlock()
			continue
		}

		// Phase 2: Dispatch check requests in parallel
		for _, entry := range g.entries {
			e := entry
			wg.Go(func() {
				req := &azdext.ValidationMessage{
					RequestId: uuid.NewString(),
					MessageType: &azdext.ValidationMessage_ValidationCheckRequest{
						ValidationCheckRequest: &azdext.ValidationCheckRequest{
							CheckType: checkType,
							RuleId:    e.RuleID,
							ContextId: contextID,
						},
					},
				}

				resp, err := e.Broker.SendAndWait(ctx, req)

				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf(
						"validation check '%s/%s' from %s: %w",
						e.CheckType, e.RuleID, e.Extension.Id, err,
					))
					mu.Unlock()
					return
				}

				// Record as invoked only after successful dispatch
				mu.Lock()
				invokedRuleIDs = append(invokedRuleIDs, e.RuleID)
				mu.Unlock()

				checkResp := resp.GetValidationCheckResponse()
				if checkResp != nil && len(checkResp.Results) > 0 {
					mu.Lock()
					allResults = append(
						allResults, checkResp.Results...,
					)
					mu.Unlock()
				}
			})
		}
	}

	wg.Wait()

	if len(errs) > 0 {
		return allResults, invokedRuleIDs, errors.Join(errs...)
	}

	return allResults, invokedRuleIDs, nil
}

// sendContextChunks delivers context data to an extension in chunks.
// Each key's data is split into chunks of validationContextChunkSize.
// The extension acks with PrepareValidationContextResponse after the
// final chunk (is_last_key=true).
func sendContextChunks(
	ctx context.Context,
	broker *grpcbroker.MessageBroker[azdext.ValidationMessage],
	contextID string,
	checkType string,
	contextData map[string][]byte,
) error {
	keys := slices.Sorted(maps.Keys(contextData))
	totalKeys := len(keys)

	totalSize := 0
	for _, v := range contextData {
		totalSize += len(v)
	}
	log.Printf(
		"sending validation context: %d keys, %d bytes total (chunk_size=%d)",
		totalKeys, totalSize, validationContextChunkSize,
	)

	for keyIdx, key := range keys {
		data := contextData[key]
		isLastKey := keyIdx == totalKeys-1

		// Handle empty data as a single zero-length chunk
		if len(data) == 0 {
			chunk := &azdext.ValidationMessage{
				RequestId: uuid.NewString(),
				MessageType: &azdext.ValidationMessage_PrepareValidationContextChunk{
					PrepareValidationContextChunk: &azdext.PrepareValidationContextChunk{
						ContextId:   contextID,
						CheckType:   checkType,
						Key:         key,
						Data:        nil,
						ChunkIndex:  0,
						IsLastChunk: true,
						IsLastKey:   isLastKey,
						TotalKeys:   int32(totalKeys),
					},
				},
			}
			if isLastKey {
				// Final chunk — wait for ack
				if _, err := broker.SendAndWait(ctx, chunk); err != nil {
					return fmt.Errorf("sending final chunk for key %q: %w", key, err)
				}
			} else {
				if err := broker.Send(ctx, chunk); err != nil {
					return fmt.Errorf("sending chunk for key %q: %w", key, err)
				}
			}
			continue
		}

		for offset := 0; offset < len(data); offset += validationContextChunkSize {
			end := min(offset+validationContextChunkSize, len(data))
			chunkIndex := int32(offset / validationContextChunkSize)
			isLastChunk := end == len(data)

			chunk := &azdext.ValidationMessage{
				RequestId: uuid.NewString(),
				MessageType: &azdext.ValidationMessage_PrepareValidationContextChunk{
					PrepareValidationContextChunk: &azdext.PrepareValidationContextChunk{
						ContextId:   contextID,
						CheckType:   checkType,
						Key:         key,
						Data:        data[offset:end],
						ChunkIndex:  chunkIndex,
						IsLastChunk: isLastChunk,
						IsLastKey:   isLastKey && isLastChunk,
						TotalKeys:   int32(totalKeys),
					},
				},
			}

			if isLastKey && isLastChunk {
				// Final chunk of final key — wait for ack
				if _, err := broker.SendAndWait(ctx, chunk); err != nil {
					return fmt.Errorf(
						"sending final context chunk: %w", err,
					)
				}
			} else {
				// Intermediate chunk — fire and forget
				if err := broker.Send(ctx, chunk); err != nil {
					return fmt.Errorf(
						"sending context chunk %d for key %q: %w",
						chunkIndex, key, err,
					)
				}
			}
		}
	}

	return nil
}
