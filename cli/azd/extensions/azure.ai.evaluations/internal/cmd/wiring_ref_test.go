// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
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
// and it is compared as a file identity rather than as text.
//
// Matching on name and host alone reported the wiring present after
// `init --path` moved the configuration, and `azd up` went on deploying the
// file left behind. Comparing the text alone would have called
// `evals/azure.eval.yaml` and `./evals/azure.eval.yaml` two different answers,
// and a relative ref and the absolute path it resolves to two different files.
func TestServiceRefIsComparedAsAPath(t *testing.T) {
	const root = "/proj"

	assert.True(t, sameRefTarget(root, "./evals/azure.eval.yaml", "evals/azure.eval.yaml"),
		"the same file written two ways is one answer")
	assert.True(t, sameRefTarget(root, "evals/../evals/azure.eval.yaml", "./evals/azure.eval.yaml"))
	assert.False(t, sameRefTarget(root, "./evals/azure.eval.yaml", "./quality/azure.eval.yaml"),
		"a different file is what the guard exists to catch")
}

// A relative `$ref` and the absolute path it resolves to name one file.
//
// `init --path` with an absolute directory wrote an absolute ref, and the next
// run compared it against the relative form and reported the service as
// pointing somewhere else -- refusing to scaffold over a configuration it had
// written itself.
func TestARelativeRefAndItsAbsolutePathAreOneFile(t *testing.T) {
	root := t.TempDir()
	absolute := filepath.Join(root, "evals", "azure.eval.yaml")

	assert.True(t, sameRefTarget(root, "./evals/azure.eval.yaml", filepath.ToSlash(absolute)))
	assert.True(t, sameRefTarget(root, filepath.ToSlash(absolute), "evals/azure.eval.yaml"))
	assert.False(t, sameRefTarget(root, "./quality/azure.eval.yaml", filepath.ToSlash(absolute)))
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

// projectLayout makes a project root with a subdirectory and stands the test in
// it, answering with the root as the process now spells it.
//
// os.Getwd resolves symlinks -- /var is /private/var on macOS -- so a root
// taken straight from t.TempDir() and a working directory taken from Getwd are
// two spellings of one place, and filepath.Rel between them walks all the way
// up and back down.
func projectLayout(t *testing.T, subdir string) string {
	t.Helper()
	sub := filepath.Join(t.TempDir(), subdir)
	require.NoError(t, os.MkdirAll(sub, 0o750))
	t.Chdir(sub)

	here, err := os.Getwd()
	require.NoError(t, err)
	root := here
	for range strings.Count(filepath.ToSlash(subdir), "/") + 1 {
		root = filepath.Dir(root)
	}
	return root
}

// A `$ref` is read relative to the directory holding azure.yaml, but --path is
// relative to wherever the caller stood. Writing the one as the other meant
// `init` run from src/api wrote the scaffold under src/api and then told
// azure.yaml it was at the project root, so `azd up` deployed a file that was
// never there -- and the scaffold the reader was looking at never deployed.
func TestRefToIsRelativeToTheProjectRootNotTheCaller(t *testing.T) {
	root := projectLayout(t, filepath.Join("src", "api"))

	got := refTo(root, filepath.Join("evals", "azure.eval.yaml"))

	assert.Equal(t, "./src/api/evals/azure.eval.yaml", got,
		"the ref has to name the file from the root it will be resolved against")
}

// Standing at the root is the case that always worked, and has to keep working.
func TestRefToFromTheProjectRootIsUnchanged(t *testing.T) {
	root := projectLayout(t, "here")

	assert.Equal(t, "./evals/azure.eval.yaml",
		refTo(filepath.Join(root, "here"), filepath.Join("evals", "azure.eval.yaml")))
}

// A configuration kept outside the project is still resolved against the root,
// so it is named from there; a `./` in front of `../` would only be noise.
func TestRefToOutsideTheProjectClimbsOutOfIt(t *testing.T) {
	root := projectLayout(t, "project")

	got := refTo(filepath.Join(root, "project"), filepath.Join("..", "shared", "azure.eval.yaml"))

	assert.Equal(t, "../shared/azure.eval.yaml", got)
}
