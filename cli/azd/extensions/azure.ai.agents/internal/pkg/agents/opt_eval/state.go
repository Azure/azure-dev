// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// state.go centralizes transient runtime state that is persisted in the azd
// environment across CLI invocations. This covers eval job tracking and any
// other cross-invocation state needed by eval, optimize, or related commands.

package opt_eval

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// EvalState holds transient runtime state stored in the azd environment
// for tracking generation job progress across CLI invocations.
type EvalState struct {
	InitStatus       string // overall init status
	DatasetGenOpID   string // dataset generation operation ID
	DatasetGenStatus string // dataset generation job status
	EvalGenOpID      string // evaluator generation operation ID
	EvalGenStatus    string // evaluator generation job status
	EvalID           string // created eval ID for running evals
}

// InitStatus values.
const (
	InitStatusPending   = "pending"
	InitStatusCompleted = "completed"
)

// Azd environment keys for persisting eval state across CLI invocations.
const (
	evalKeyInitStatus       = "LAST_EVAL_INIT_STATUS"
	evalKeyDatasetGenOpID   = "LAST_EVAL_DATASET_GEN_OP_ID"
	evalKeyDatasetGenStatus = "LAST_EVAL_DATASET_GEN_STATUS"
	evalKeyEvalGenOpID      = "LAST_EVAL_GEN_OP_ID"
	evalKeyEvalGenStatus    = "LAST_EVAL_GEN_STATUS"
	evalKeyEvalID           = "LAST_EVAL_ID"
)

// LoadEvalState reads eval runtime state from the azd environment.
// Individual key-read errors are logged but do not prevent loading
// the remaining keys; a partial state is still useful for resume logic.
func LoadEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string) *EvalState {
	get := func(key string) string {
		v, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName, Key: key,
		})
		if err != nil {
			log.Printf("LoadEvalState: failed to read %s: %v", key, err)
			return ""
		}
		return v.Value
	}
	return &EvalState{
		InitStatus:       get(evalKeyInitStatus),
		DatasetGenOpID:   get(evalKeyDatasetGenOpID),
		DatasetGenStatus: get(evalKeyDatasetGenStatus),
		EvalGenOpID:      get(evalKeyEvalGenOpID),
		EvalGenStatus:    get(evalKeyEvalGenStatus),
		EvalID:           get(evalKeyEvalID),
	}
}

// SaveEvalState persists eval runtime state to the azd environment.
func SaveEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string, state *EvalState) error {
	pairs := []struct {
		key, val string
	}{
		{evalKeyInitStatus, state.InitStatus},
		{evalKeyDatasetGenOpID, state.DatasetGenOpID},
		{evalKeyDatasetGenStatus, state.DatasetGenStatus},
		{evalKeyEvalGenOpID, state.EvalGenOpID},
		{evalKeyEvalGenStatus, state.EvalGenStatus},
		{evalKeyEvalID, state.EvalID},
	}
	for _, p := range pairs {
		if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: p.key, Value: p.val,
		}); err != nil {
			return fmt.Errorf("setting %s in azd env: %w", p.key, err)
		}
	}
	return nil
}

// ClearEvalState removes eval state keys from the azd environment.
func ClearEvalState(ctx context.Context, azdClient *azdext.AzdClient, envName string) error {
	var errs []error
	for _, key := range []string{
		evalKeyInitStatus, evalKeyDatasetGenOpID, evalKeyDatasetGenStatus,
		evalKeyEvalGenOpID, evalKeyEvalGenStatus, evalKeyEvalID,
	} {
		_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: key, Value: "",
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
