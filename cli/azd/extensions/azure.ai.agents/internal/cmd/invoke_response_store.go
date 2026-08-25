// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const backgroundResponsesConfigPath = configPathPrefix + ".backgroundResponses"

type savedBackgroundResponse struct {
	ResponseID string `json:"responseId"`
	// LastSequenceNumber is the sequence_number of the last fully processed
	// Responses SSE event. It is sent as starting_after when the stream is resumed.
	// A pointer preserves zero, which is a valid sequence number.
	LastSequenceNumber *int64 `json:"cursor,omitempty"`
	// Status is the Responses API response.status value: queued, in_progress,
	// completed, failed, incomplete, or cancelled.
	Status         string `json:"status,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
}

type responseStateStore interface {
	Get(ctx context.Context, agentKey string) (*savedBackgroundResponse, error)
	Save(ctx context.Context, agentKey string, record savedBackgroundResponse) error
	Delete(ctx context.Context, agentKey string) error
}

type userConfigResponseStateStore struct {
	client *azdext.AzdClient
}

func newUserConfigResponseStateStore(client *azdext.AzdClient) responseStateStore {
	return &userConfigResponseStateStore{client: client}
}

func (s *userConfigResponseStateStore) Get(ctx context.Context, agentKey string) (*savedBackgroundResponse, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create background response config helper: %w", err)
	}

	var records map[string]savedBackgroundResponse
	found, err := config.GetUserJSON(ctx, backgroundResponsesConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read background responses: %w", err)
	}
	if !found || records == nil {
		return nil, nil
	}

	record, ok := records[agentKey]
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (s *userConfigResponseStateStore) Save(
	ctx context.Context,
	agentKey string,
	record savedBackgroundResponse,
) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create background response config helper: %w", err)
	}

	var records map[string]savedBackgroundResponse
	found, err := config.GetUserJSON(ctx, backgroundResponsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read background responses: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedBackgroundResponse)
	}

	if current, ok := records[agentKey]; ok && current.ResponseID == record.ResponseID &&
		current.LastSequenceNumber != nil &&
		(record.LastSequenceNumber == nil || *record.LastSequenceNumber < *current.LastSequenceNumber) {
		record.LastSequenceNumber = new(*current.LastSequenceNumber)
	}
	records[agentKey] = record

	if err := config.SetUserJSON(ctx, backgroundResponsesConfigPath, records); err != nil {
		return fmt.Errorf("write background responses: %w", err)
	}
	return nil
}

func (s *userConfigResponseStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create background response config helper: %w", err)
	}

	var records map[string]savedBackgroundResponse
	found, err := config.GetUserJSON(ctx, backgroundResponsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read background responses: %w", err)
	}
	if !found || records == nil {
		return nil
	}

	delete(records, agentKey)
	if err := config.SetUserJSON(ctx, backgroundResponsesConfigPath, records); err != nil {
		return fmt.Errorf("write background responses: %w", err)
	}
	return nil
}
