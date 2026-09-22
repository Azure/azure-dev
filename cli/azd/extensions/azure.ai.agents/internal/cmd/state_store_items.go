// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// maxStateStoreInputBytes uses the documented value ceiling to bound local buffering
// without an arbitrary multiplier. This conservatively counts raw whitespace too:
// callers must compact larger formatted inputs before passing them to the CLI.
const maxStateStoreInputBytes = agent_api.MaxStateStoreValueBytes

var errStateStoreInputTooLarge = fmt.Errorf(
	"item input exceeds the raw JSON input limit of %d bytes (1 MiB)", maxStateStoreInputBytes,
)

func readStateStoreValue(
	ctx context.Context, flags *stateStoreFlags, stdin io.Reader,
) (agent_api.SetStateStoreItemRequest, error) {
	var request agent_api.SetStateStoreItemRequest
	var err error
	switch {
	case flags.valueFile == "-":
		request.Value, err = readStateStoreInput(ctx, stdin)
	case flags.valueFile != "":
		// The caller explicitly selected this input file; it is not a derived path.
		var file *os.File
		file, err = os.Open(flags.valueFile)
		if err == nil {
			defer file.Close()
			request.Value, err = readStateStoreInput(ctx, file)
		}
	case len(flags.value) > maxStateStoreInputBytes:
		err = errStateStoreInputTooLarge
	default:
		request.Value = json.RawMessage(flags.value)
	}
	if err != nil {
		if exterrors.IsCancellation(err) {
			return request, exterrors.Cancelled("reading item value cancelled")
		}
		if errors.Is(err, errStateStoreInputTooLarge) {
			return request, exterrors.Validation(exterrors.CodeInvalidParameter, err.Error(),
				"compact the JSON if whitespace makes it too large; otherwise reduce the item value")
		}
		return request, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("could not read --value-file: %v", err), "check the input path or stdin and retry")
	}
	if err := agent_api.ValidateStateStoreValue(request.Value); err != nil {
		suggestion := "provide a JSON object as the value, not a REST request envelope"
		if errors.Is(err, agent_api.ErrStateStoreValueTooLarge) {
			suggestion = "reduce the item value; JSON escaping can increase its serialized size"
		}
		return request, exterrors.Validation(exterrors.CodeInvalidParameter, err.Error(), suggestion)
	}
	if len(flags.tags) > 0 {
		request.Tags = make(map[string]string, len(flags.tags))
	}
	for _, tag := range flags.tags {
		key, value, found := strings.Cut(tag, "=")
		if !found || key == "" {
			return request, exterrors.Validation(exterrors.CodeInvalidParameter,
				"invalid --tag", "use --tag key=value with a non-empty key")
		}
		if _, exists := request.Tags[key]; exists {
			return request, exterrors.Validation(exterrors.CodeInvalidParameter,
				"duplicate --tag key", "provide each tag key only once")
		}
		request.Tags[key] = value
	}
	return request, nil
}

// readStateStoreInput closes files and pipes (including stdin) on cancellation.
// Waiting for the close callback prevents a late callback from racing with caller cleanup.
func readStateStoreInput(ctx context.Context, reader io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if closer, ok := reader.(io.Closer); ok {
		closed := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			defer close(closed)
			_ = closer.Close()
		})
		defer func() {
			if !stop() {
				<-closed
			}
		}()
	}
	// Read one extra byte to distinguish an exact-limit input from a truncated one.
	data, err := io.ReadAll(io.LimitReader(reader, maxStateStoreInputBytes+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(data) > maxStateStoreInputBytes {
		return nil, errStateStoreInputTooLarge
	}
	return data, err
}

func (a *stateStoreAction) executeItem(ctx context.Context, operation, key string) (any, error) {
	store, err := a.resolveStore(ctx, a.flags.store)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "items list":
		return a.api.ListStateStoreItemKeys(ctx, a.target.Name, store, a.flags.page)
	case "items show":
		return a.api.GetStateStoreItem(ctx, a.target.Name, store, key)
	case "items set":
		return a.api.SetStateStoreItem(ctx, a.target.Name, store, key, a.request, a.flags.ifMatch)
	case "items delete":
		if !a.flags.yes {
			if a.flags.noPrompt {
				return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
					"--yes is required with --no-prompt", "supply --yes to approve deletion")
			}
			confirmation, err := a.prompt.Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
				Message: fmt.Sprintf("Delete item %q from store %q on agent %q (%s)?",
					key, store, a.target.Name, a.target.ProjectEndpoint),
				DefaultValue: new(false),
			}})
			if err != nil {
				return nil, exterrors.FromPrompt(err, "confirming item deletion")
			}
			if confirmation == nil || confirmation.Value == nil || !*confirmation.Value {
				return nil, exterrors.Cancelled("item deletion cancelled")
			}
		}
		return a.api.DeleteStateStoreItem(ctx, a.target.Name, store, key, a.flags.ifMatch)
	default:
		return nil, fmt.Errorf("unknown State Store operation")
	}
}
