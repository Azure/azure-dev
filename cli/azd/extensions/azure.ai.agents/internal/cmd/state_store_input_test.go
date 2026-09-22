// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestStateStoreInputBoundaries(t *testing.T) {
	for _, size := range []int{maxStateStoreInputBytes - 1, maxStateStoreInputBytes, maxStateStoreInputBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data, err := readStateStoreInput(t.Context(), strings.NewReader(strings.Repeat(" ", size)))
			if size > maxStateStoreInputBytes {
				require.ErrorIs(t, err, errStateStoreInputTooLarge)
				require.Nil(t, data, "never return truncated input as a usable value")
			} else {
				require.NoError(t, err)
				require.Len(t, data, size)
			}
		})
	}
}

// stateStoreUnexpectedReader fails instead of blocking or allocating indefinitely if the
// bounded reader ever asks for more input. It lets the test assert that no EOF is needed.
type stateStoreUnexpectedReader struct{ t *testing.T }

func (r stateStoreUnexpectedReader) Read([]byte) (int, error) {
	r.t.Error("read beyond the safety budget plus one byte")
	return 0, io.ErrUnexpectedEOF
}

func TestStateStoreInputStopsWithoutEOF(t *testing.T) {
	reader := io.MultiReader(
		strings.NewReader(strings.Repeat(" ", maxStateStoreInputBytes+1)),
		stateStoreUnexpectedReader{t},
	)
	data, err := readStateStoreInput(t.Context(), reader)
	require.ErrorIs(t, err, errStateStoreInputTooLarge)
	require.Nil(t, data)
}

func TestStateStoreInputAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readStateStoreInput(ctx, stateStoreUnexpectedReader{t})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStateStoreValueInputBudget(t *testing.T) {
	for _, size := range []int{2 * 1024 * 1024, maxStateStoreInputBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// A valid object above 1 MiB must not be rejected as though the inline service
			// limit applied universally. The service performs its own serialized-size check.
			value := `{"payload":"` + strings.Repeat("a", size-len(`{"payload":""}`)) + `"}`
			file := filepath.Join(t.TempDir(), "value.json")
			require.NoError(t, os.WriteFile(file, []byte(value), 0600))
			for _, source := range []string{"inline", "file", "stdin"} {
				t.Run(source, func(t *testing.T) {
					flags := &stateStoreFlags{}
					switch source {
					case "inline":
						flags.value = value
					case "file":
						flags.valueFile = file
					case "stdin":
						flags.valueFile = "-"
					}
					request, err := readStateStoreValue(t.Context(), flags, strings.NewReader(value))
					if size > maxStateStoreInputBytes {
						local, ok := errors.AsType[*azdext.LocalError](err)
						require.True(t, ok, "expected validation error, got %v", err)
						require.Equal(t, exterrors.CodeInvalidParameter, local.Code)
						require.Contains(t, local.Message, "CLI safety limit of 16 MiB")
						require.Contains(t, local.Suggestion, "reduce the raw input size")
						require.Nil(t, request.Value)
					} else {
						require.NoError(t, err)
						require.Len(t, request.Value, size)
					}
				})
			}
		})
	}
}

func TestStateStoreOversizedInputNeverResolvesTarget(t *testing.T) {
	for _, source := range []string{"file", "stdin"} {
		t.Run(source, func(t *testing.T) {
			factoryCalled := false
			cmd := newStateStoreCommandWithFactory(&azdext.ExtensionContext{}, "items set",
				func(context.Context, *stateStoreFlags) (*stateStoreAction, func(), error) {
					factoryCalled = true
					return nil, nil, errors.New("unexpected target resolution")
				})
			input := strings.Repeat(" ", maxStateStoreInputBytes+1)
			path := "-"
			if source == "file" {
				path = filepath.Join(t.TempDir(), "oversized.json")
				require.NoError(t, os.WriteFile(path, []byte(input), 0600))
			}
			cmd.SetIn(strings.NewReader(input))
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"size-probe", "--value-file", path})
			require.ErrorContains(t, cmd.ExecuteContext(t.Context()), "CLI safety limit of 16 MiB")
			require.False(t, factoryCalled, "oversized input must fail before configuration, auth, or API calls")
		})
	}
}
