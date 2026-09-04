// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func serviceWithRef(t *testing.T, ref string) *azdext.ServiceConfig {
	t.Helper()
	if ref == "" {
		return &azdext.ServiceConfig{Name: "evals"}
	}
	props, err := structpb.NewStruct(map[string]any{"$ref": ref})
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "evals", AdditionalProperties: props}
}

// The `$ref` an entry already carries decides whether the wiring is present,
// and it is compared as a path rather than as text.
//
// Matching on name and host alone reported the wiring present after
// `init --path` moved the configuration, and `azd up` went on deploying the
// file left behind. Comparing the text alone would have called
// `evals/azure.eval.yaml` and `./evals/azure.eval.yaml` two different answers.
func TestServiceRefIsComparedAsAPath(t *testing.T) {
	assert.True(t, sameRefTarget("./evals/azure.eval.yaml", "evals/azure.eval.yaml"),
		"the same file written two ways is one answer")
	assert.True(t, sameRefTarget("evals/../evals/azure.eval.yaml", "./evals/azure.eval.yaml"))
	assert.False(t, sameRefTarget("./evals/azure.eval.yaml", "./quality/azure.eval.yaml"),
		"a different file is what the guard exists to catch")
}

// An entry with no `$ref` has nothing to disagree with.
func TestServiceConfigRefReadsTheDeclaredValue(t *testing.T) {
	assert.Equal(t, "./evals/azure.eval.yaml",
		serviceConfigRef(serviceWithRef(t, "./evals/azure.eval.yaml")))
	assert.Empty(t, serviceConfigRef(serviceWithRef(t, "")))
}

// The refusal names both paths, because the reader is the one who has to decide
// which of the two configurations they meant to keep.
func TestServiceRefConflictNamesBothPaths(t *testing.T) {
	err := messages.ServiceRefPointsElsewhere(
		"support-agent-evals", "./evals/azure.eval.yaml", "./quality/azure.eval.yaml")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "./evals/azure.eval.yaml")
	assert.Contains(t, err.Error(), "./quality/azure.eval.yaml")
	assert.Contains(t, err.Error(), "support-agent-evals")
}
