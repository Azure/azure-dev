// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAgentStateStoreRoundTrip(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = "session-1"
		record.ConversationID = "conversation-1"
		record.Response = &savedResponse{ResponseID: "response-1"}
		record.Invocation = &savedInvocation{InvocationID: "invocation-1"}
		record.StateStore = "checkpoints"
	}))

	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "session-1", record.SessionID)
	assert.Equal(t, "conversation-1", record.ConversationID)
	assert.Equal(t, "response-1", record.Response.ResponseID)
	assert.Equal(t, "invocation-1", record.Invocation.InvocationID)
	assert.Equal(t, "checkpoints", record.StateStore)

	data, err := os.ReadFile(store.statePath("agent-a"))
	require.NoError(t, err)
	var persisted agentStateRecord
	require.NoError(t, json.Unmarshal(data, &persisted))
	assert.Equal(t, "agent-a", persisted.AgentKey)
	assert.NotContains(t, store.statePath("agent-a"), "agent-a")
}

func TestAgentStateStoreConcurrentFieldUpdates(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)

	results := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Go(func() {
		results <- store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.SessionID = "session-1"
		})
	})
	wg.Go(func() {
		results <- store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.ConversationID = "conversation-1"
		})
	})
	wg.Go(func() {
		results <- store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
			record.Response = &savedResponse{ResponseID: "response-1"}
		})
	})
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}

	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "session-1", record.SessionID)
	assert.Equal(t, "conversation-1", record.ConversationID)
	assert.Equal(t, "response-1", record.Response.ResponseID)
}

func TestAgentStateStoreDeletesEmptyRecord(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = "session-1"
	}))
	require.NoError(t, store.mutate(t.Context(), "agent-a", func(record *agentStateRecord) {
		record.SessionID = ""
	}))

	_, err = os.Stat(store.statePath("agent-a"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	assert.Nil(t, record)
}

func TestAgentStateStoreRejectsCorruptState(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.statePath("agent-a"), []byte("{"), 0o600))

	_, err = store.get(t.Context(), "agent-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse agent state")
}

func TestAgentStateStoreRejectsNonRegularLock(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(store.lockPath("agent-a"), 0o700))

	_, err = store.get(t.Context(), "agent-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a regular file")
}

func TestAgentStateStoreDoesNotUseUserConfig(t *testing.T) {
	t.Parallel()

	client := newInvokeTestAzdClient(t, &azdext.UnimplementedUserConfigServiceServer{})
	const agentKey = "example.com/agents/worker/versions/1/remote"
	saveAgentStateForTest(t, client, agentKey, agentStateRecord{SessionID: "initial-session"})
	for field, value := range map[string]string{
		"sessions": "session-1", "conversations": "conversation-1", "stateStores": "checkpoints",
	} {
		require.NoError(t, setAgentSpecificContextValue(t.Context(), client, field, agentKey, value))
		got, err := getAgentSpecificContextValue(t.Context(), client, field, agentKey)
		require.NoError(t, err)
		assert.Equal(t, value, got)
	}

	responses := newResponseStateStore(client)
	require.NoError(t, responses.Save(t.Context(), agentKey, savedResponse{ResponseID: "response-1"}))
	response, err := responses.Get(t.Context(), agentKey)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "response-1", response.ResponseID)

	invocations := newInvocationStateStore(client)
	require.NoError(t, invocations.Save(t.Context(), agentKey, savedInvocation{InvocationID: "invocation-1"}))
	invocation, err := invocations.Get(t.Context(), agentKey)
	require.NoError(t, err)
	require.NotNil(t, invocation)
	assert.Equal(t, "invocation-1", invocation.InvocationID)

	for _, field := range []string{"sessions", "conversations", "stateStores"} {
		require.NoError(t, deleteContextValue(t.Context(), client, field, agentKey))
	}
	require.NoError(t, responses.Delete(t.Context(), agentKey))
	require.NoError(t, invocations.Delete(t.Context(), agentKey))
	assert.Nil(t, loadAgentStateForTest(t, client, agentKey))
}

func TestClassifyAgentStateReadError(t *testing.T) {
	t.Parallel()

	for _, classifier := range []struct {
		name string
		call func(error) error
		code string
	}{
		{"Invocation", classifyInvocationStateReadError, exterrors.CodeInvalidInvocationState},
		{"Response", classifyResponseStateReadError, exterrors.CodeInvalidResponseState},
	} {
		t.Run(classifier.name, func(t *testing.T) {
			t.Parallel()
			for _, cause := range []error{
				context.Canceled,
				fmt.Errorf("read state: %w", context.Canceled),
				status.Error(codes.Canceled, "lock wait cancelled"),
			} {
				err := classifier.call(cause)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeCancelled, local.Code)
				assert.Equal(t, azdext.LocalErrorCategoryUser, local.Category)
			}
			err := classifier.call(errors.New("invalid JSON"))
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, classifier.code, local.Code)
		})
	}
}

func TestAgentStateStoreCancelledLockWait(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	lock := flock.New(store.lockPath("agent-a"))
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { require.NoError(t, lock.Unlock()) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := store.get(ctx, "agent-a")
		result <- err
	}()
	require.Never(t, func() bool {
		select {
		case <-result:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond, "read must wait for the held lock")
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled lock wait did not return")
	}
}

func TestAgentStateStoreCrossProcessUpdates(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		workers.Wait()
	})
	results := make(chan error, 2)
	startWorker := func(field string) {
		workers.Go(func() {
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAgentStateStoreWorker$", "--", root, field)
			output, err := cmd.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("%s worker: %w\n%s", field, err, output)
			}
			results <- err
		})
	}

	startWorker("session")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "session.locked"))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	startWorker("conversation")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "conversation.ready"))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "conversation.locked"))
		return err == nil
	}, 150*time.Millisecond, 10*time.Millisecond, "second process must wait for the first process's lock")
	require.NoError(t, os.WriteFile(filepath.Join(root, "release-session"), nil, 0o600))
	for range 2 {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("agent state workers did not finish")
		}
	}

	store, err := newAgentStateStoreAt(root)
	require.NoError(t, err)
	record, err := store.get(t.Context(), "shared-agent")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "session-7", record.SessionID)
	assert.Equal(t, "conversation-7", record.ConversationID)
}

func TestAgentStateStoreWorker(t *testing.T) {
	marker := slices.Index(os.Args, "--")
	if marker == -1 {
		return
	}
	require.Len(t, os.Args[marker+1:], 2)
	root, field := os.Args[marker+1], os.Args[marker+2]
	require.Contains(t, []string{"session", "conversation"}, field)
	store, err := newAgentStateStoreAt(root)
	require.NoError(t, err)
	//nolint:gosec // G703: root is the parent's temporary directory; field is allow-listed above.
	require.NoError(t, os.WriteFile(filepath.Join(root, field+".ready"), nil, 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for i := range 8 {
		var coordinationErr error
		err := store.mutate(ctx, "shared-agent", func(record *agentStateRecord) {
			if i == 0 {
				//nolint:gosec // G703: root is the parent's temporary directory; field is allow-listed above.
				coordinationErr = os.WriteFile(filepath.Join(root, field+".locked"), nil, 0o600)
				if field == "session" && coordinationErr == nil {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						//nolint:gosec // G703: this coordination file is inside the parent's temporary directory.
						if _, err := os.Stat(filepath.Join(root, "release-session")); err == nil {
							break
						} else if !errors.Is(err, os.ErrNotExist) {
							coordinationErr = err
							return
						}
						select {
						case <-ticker.C:
						case <-ctx.Done():
							coordinationErr = ctx.Err()
							return
						}
					}
				}
			}
			switch field {
			case "session":
				record.SessionID = fmt.Sprintf("session-%d", i)
			case "conversation":
				record.ConversationID = fmt.Sprintf("conversation-%d", i)
			}
		})
		require.NoError(t, err)
		require.NoError(t, coordinationErr)
	}
}

func TestAgentStateStoreDeleteCorruptRecord(t *testing.T) {
	client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
	key := buildAgentKey("https://example.com/projects/project", "agent", "1", false)
	store, err := newAgentStateStoreForClient(client)
	require.NoError(t, err)
	selectionKey := normalizeEndpoint("https://example.com/projects/project") + "/agents/agent"
	for _, agentKey := range []string{key, selectionKey} {
		require.NoError(t, os.WriteFile(store.statePath(agentKey), []byte("{"), 0o600))
	}
	require.True(t, cleanupAgentStateForKey(t.Context(), client, key))
	for _, agentKey := range []string{key, selectionKey} {
		_, err := os.Stat(store.statePath(agentKey))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, store.delete(t.Context(), key), "deleting missing state is idempotent")
}

func TestAgentStateStoreDoesNotDiscloseCredentials(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	const endpoint = "https://secret-user:secret-password@HOST.example/agents/agent/versions/1" +
		"?sig=secret-signature#secret-fragment"
	key := buildRemoteAgentKeyFromEndpoint(endpoint)
	cleanKey := buildRemoteAgentKeyFromEndpoint("https://host.example/agents/agent/versions/1")
	assert.Equal(t, cleanKey, key)
	rawKey := "secret-user:secret-password@HOST.example/agents/agent/versions/1/remote" +
		"?sig=secret-signature#secret-fragment"
	require.NoError(t, store.mutate(t.Context(), rawKey, func(record *agentStateRecord) {
		record.SessionID = "session-1"
	}))
	record, err := store.get(t.Context(), cleanKey)
	require.NoError(t, err)
	require.NotNil(t, record)
	data, err := os.ReadFile(store.statePath(cleanKey))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.statePath(cleanKey), []byte("{"), 0o600))
	_, readErr := store.get(t.Context(), rawKey)
	require.Error(t, readErr)
	require.NoError(t, os.Remove(store.lockPath(cleanKey)))
	require.NoError(t, os.Mkdir(store.lockPath(cleanKey), 0o700))
	_, lockErr := store.get(t.Context(), rawKey)
	require.Error(t, lockErr)
	for _, secret := range []string{"secret-user", "secret-password", "sig=", "secret-signature", "secret-fragment"} {
		assert.NotContains(t, string(data), secret)
		assert.NotContains(t, readErr.Error(), secret)
		assert.NotContains(t, lockErr.Error(), secret)
	}
}

func TestAgentStateStoreDeleteWaitsForMutation(t *testing.T) {
	store, err := newAgentStateStoreAt(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		workers.Wait()
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	saved := make(chan error, 1)
	workers.Go(func() {
		saved <- store.mutate(ctx, "agent-a", func(record *agentStateRecord) {
			record.SessionID = "session-1"
			record.ConversationID = "conversation-1"
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("mutation did not acquire the agent lock")
	}
	deleted := make(chan error, 1)
	workers.Go(func() {
		deleted <- store.delete(ctx, "agent-a")
	})
	require.Never(t, func() bool {
		select {
		case <-deleted:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond)
	close(release)
	require.NoError(t, <-saved)
	require.NoError(t, <-deleted)
	record, err := store.get(t.Context(), "agent-a")
	require.NoError(t, err)
	assert.Nil(t, record, "delete must remove the whole record after the concurrent save")
}

func TestResolveStoredIDFromAgentState(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		saved      string
		explicit   string
		forceNew   bool
		generate   bool
		want       string
		wantStored string
	}{
		{name: "reuse", saved: "saved-session", want: "saved-session", wantStored: "saved-session"},
		{name: "explicit", saved: "saved-session", explicit: "explicit-session",
			want: "explicit-session", wantStored: "explicit-session"},
		{name: "new remote session", saved: "saved-session", forceNew: true, wantStored: "saved-session"},
		{name: "missing remote session"},
		{name: "generate local session", generate: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newInvokeTestAzdClient(t, &azdext.UnimplementedUserConfigServiceServer{})
			const agentKey = "example.com/agents/worker/versions/1/remote"
			if tt.saved != "" {
				saveAgentStateForTest(t, client, agentKey, agentStateRecord{SessionID: tt.saved})
			}
			got, err := resolveStoredID(t.Context(), client, agentKey, tt.explicit, tt.forceNew, "sessions", tt.generate)
			require.NoError(t, err)
			wantStored := tt.wantStored
			if tt.generate {
				require.Len(t, got, 36)
				wantStored = got
			} else {
				assert.Equal(t, tt.want, got)
			}
			stored, err := getAgentSpecificContextValue(t.Context(), client, "sessions", agentKey)
			require.NoError(t, err)
			assert.Equal(t, wantStored, stored)
		})
	}
}
