// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func loadAgentStateForTest(t *testing.T, client *azdext.AzdClient, agentKey string) *agentStateRecord {
	t.Helper()
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	record, err := store.get(t.Context(), agentKey)
	require.NoError(t, err)
	return record
}

func saveAgentStateForTest(t *testing.T, client *azdext.AzdClient, agentKey string, record agentStateRecord) {
	t.Helper()
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	store.client = nil
	record.Version = agentStateSchemaVersion
	record.AgentKey = agentKey
	require.NoError(t, store.mutate(t.Context(), agentKey, func(current *agentStateRecord) {
		*current = record
	}))
}
