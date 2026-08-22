// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Whether this configuration owns an evaluator is decided in exactly one place.
//
// Two publish loops ask it -- `azd up` through CustomEvaluators, and `eval
// create` over the evaluators one eval names. Each carried its own copy of the
// test, and the first version of both read `source == ""`. That is wrong in a
// way nothing surfaces: validation forbids declaring the rubric twice, so an
// evaluator carrying its rubric under `definition:` has an empty `source`, was
// read as "nothing local to publish", and was skipped. The eval was then created
// bound to an evaluator the service had never been told about.
//
// A second copy of the predicate is how that returns, and it returns quietly --
// the config still decodes, the deploy still reports success. So the shape is
// worth failing the build over rather than trusting a reviewer to spot it.
func TestEvaluatorOwnershipIsDecidedInOnePlace(t *testing.T) {
	const predicate = "CarriesItsRubric"

	// Either half of the pair, written inline. `Definition == nil` on its own is
	// legitimate -- the reconciler branches on it to choose what to publish --
	// so it is the pairing with a `Source` test that means someone has
	// re-derived ownership.
	sightings := map[string][]int{}
	require.NoError(t, filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil // including this file, which spells out the shape it is looking for
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		insidePredicate := false
		for i, line := range strings.Split(string(body), "\n") {
			// The predicate is the one place allowed to say this, so skip its body.
			if strings.Contains(line, predicate+"() bool {") {
				insidePredicate = true
				continue
			}
			if insidePredicate {
				if strings.HasPrefix(line, "}") {
					insidePredicate = false
				}
				continue
			}
			source := strings.Contains(line, `Source == ""`) || strings.Contains(line, `Source != ""`)
			definition := strings.Contains(line, "Definition == nil") || strings.Contains(line, "Definition != nil")
			if source && definition {
				sightings[path] = append(sightings[path], i+1)
			}
		}
		return nil
	}))

	assert.Empty(t, sightings,
		"ownership is re-derived at %v; call %s instead, or the next rule about "+
			"what this configuration publishes will land on one loop and not the other",
		sightings, predicate)
}
