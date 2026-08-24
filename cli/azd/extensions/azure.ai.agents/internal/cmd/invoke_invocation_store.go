// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const invocationsConfigPath = configPathPrefix + ".invocations"

type savedInvocation struct {
	InvocationID string `json:"invocationId"`
	SessionID    string `json:"sessionId,omitempty"`
	APIVersion   string `json:"apiVersion,omitempty"`
}

type invocationStateStore struct {
	client *azdext.AzdClient
}

func newInvocationStateStore(client *azdext.AzdClient) *invocationStateStore {
	return &invocationStateStore{client: client}
}

func (s *invocationStateStore) Get(ctx context.Context, agentKey string) (*savedInvocation, error) {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return nil, fmt.Errorf("read saved invocations: %w", err)
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

func (s *invocationStateStore) Save(ctx context.Context, agentKey string, record savedInvocation) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read saved invocations: %w", err)
	}
	if !found || records == nil {
		records = make(map[string]savedInvocation)
	}
	records[agentKey] = record
	if err := config.SetUserJSON(ctx, invocationsConfigPath, records); err != nil {
		return fmt.Errorf("write saved invocations: %w", err)
	}
	return nil
}

func (s *invocationStateStore) Delete(ctx context.Context, agentKey string) error {
	config, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return fmt.Errorf("create invocation config helper: %w", err)
	}
	var records map[string]savedInvocation
	found, err := config.GetUserJSON(ctx, invocationsConfigPath, &records)
	if err != nil {
		return fmt.Errorf("read saved invocations: %w", err)
	}
	if !found || records == nil {
		return nil
	}
	delete(records, agentKey)
	if err := config.SetUserJSON(ctx, invocationsConfigPath, records); err != nil {
		return fmt.Errorf("write saved invocations: %w", err)
	}
	return nil
}
