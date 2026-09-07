// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	responsesConfigPath                 = configPathPrefix + ".responses"
	legacyBackgroundResponsesConfigPath = configPathPrefix + ".backgroundResponses"
)

type savedResponse struct {
	ResponseID string `json:"responseId"`
}

type responseStateStore interface {
	Get(ctx context.Context, agentKey string) (*savedResponse, error)
	Save(ctx context.Context, agentKey string, record savedResponse) error
	Delete(ctx context.Context, agentKey string) error
}

type userConfigResponseStateStore struct {
	client *azdext.AzdClient
}

func newUserConfigResponseStateStore(client *azdext.AzdClient) responseStateStore {
	return &userConfigResponseStateStore{client: client}
}

func (s *userConfigResponseStateStore) Get(ctx context.Context, agentKey string) (*savedResponse, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read responses: %w", err)
	}
	if found && records != nil {
		if record, ok := records[agentKey]; ok {
			return &record, nil
		}
	}

	// Read the preview schema written by versions containing PR #9703.
	// Unknown cursor/status/context fields are intentionally ignored. Fall back
	// per key so one newly saved agent does not hide legacy state for another.
	records = nil
	found, err = config.GetUserJSON(ctx, legacyBackgroundResponsesConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read legacy background responses: %w", err)
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

func (s *userConfigResponseStateStore) Save(ctx context.Context, agentKey string, record savedResponse) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	var records map[string]savedResponse
	found, err := config.GetUserJSON(ctx, responsesConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read responses: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedResponse)
	}
	records[agentKey] = record

	if err := config.SetUserJSON(ctx, responsesConfigPath, records); err != nil {
		return fmt.Errorf("write responses: %w", err)
	}
	return nil
}

func (s *userConfigResponseStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create response config helper: %w", err)
	}

	for _, path := range []string{responsesConfigPath, legacyBackgroundResponsesConfigPath} {
		var records map[string]savedResponse
		found, err := config.GetUserJSON(ctx, path, &records)
		if err != nil {
			return fmt.Errorf("read responses at %s: %w", path, err)
		}
		if !found || records == nil {
			continue
		}
		delete(records, agentKey)
		if err := config.SetUserJSON(ctx, path, records); err != nil {
			return fmt.Errorf("write responses at %s: %w", path, err)
		}
	}
	return nil
}
