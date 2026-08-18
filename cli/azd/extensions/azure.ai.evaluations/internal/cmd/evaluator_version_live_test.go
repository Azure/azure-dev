// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// Evaluator versions are the unit an eval binds to, and the service assigns
// them. This proves the extension never hands back a version it has quietly
// overwritten.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/require"
)

// TestLiveEvaluatorUpdateAlwaysPublishesANewVersion covers the shape of a
// first authoring session: create a rubric, look at it, change one weight,
// update.
//
// For a few seconds after a publish the service can answer the next one with
// the version it just assigned, writing over it rather than adding one.
// Nothing observable marks the end of that race — the version listing lags a
// publish as well, answering 404 immediately after a create — so the guard
// is the document the caller already read: it says which version exists and
// when it was written.
//
// Without it, `evaluator update` run straight after `evaluator create` reports
// success, leaves a single version holding the second rubric, and every eval
// bound to the first scores against a rubric nobody chose.
func TestLiveEvaluatorUpdateAlwaysPublishesANewVersion(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := fmt.Sprintf("azdlive-version-%d", time.Now().UnixNano())

	rubric := func(weight int) json.RawMessage {
		body, err := normalizeRubricBody(name, []byte(fmt.Sprintf(
			`{"dimensions":[{"id":"tone","weight":%d,"description":"polite"}]}`, weight)))
		require.NoError(t, err)
		return body
	}

	first, err := client.CreateEvaluatorVersion(ctx, name, rubric(1), nil, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, first.Version)
	t.Cleanup(func() {
		for _, v := range []string{first.Version, "1", "2"} {
			_ = client.DeleteEvaluatorVersion(
				context.Background(), name, v, ProjectEndpointAPIVersion)
		}
	})

	// Deliberately immediate, and passing what the caller holds rather than
	// re-reading: this is the window the guard exists for, and a test that
	// waited first would pass with the guard removed.
	previous, err := json.Marshal(first)
	require.NoError(t, err)

	started := time.Now()
	second, err := client.CreateEvaluatorVersion(
		ctx, name, rubric(2), previous, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	require.NotEqual(t, first.Version, second.Version,
		"an update issued inside the race must still publish a new version")
	t.Logf("the second version was assigned after %s", time.Since(started).Round(time.Millisecond))

	// The new version holds the new rubric, and both versions are readable.
	// The earlier one is not asserted on: if the service does collide, the
	// attempt that collided has already written the new definition over it,
	// and no amount of care on this side can undo that.
	require.Equal(t, 2, liveRubricWeight(t, client, name, second.Version))
	require.NotZero(t, liveRubricWeight(t, client, name, first.Version),
		"version %s must remain readable", first.Version)
}

// liveRubricWeight reads back the one weight the fixture rubric carries.
//
// Read as JSON rather than matched as a substring: the service reformats what
// it stores, so `"weight":1` goes in and `"weight": 1` comes back, and a
// substring assertion would fail for a reason that has nothing to do with what
// is being tested.
func liveRubricWeight(
	t *testing.T,
	client *eval_api.EvalClient,
	name, version string,
) int {
	t.Helper()

	raw, err := client.GetEvaluatorRaw(
		context.Background(), name, version, ProjectEndpointAPIVersion)
	require.NoError(t, err)

	var doc struct {
		Definition struct {
			Dimensions []struct {
				ID     string `json:"id"`
				Weight int    `json:"weight"`
			} `json:"dimensions"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Len(t, doc.Definition.Dimensions, 1)
	return doc.Definition.Dimensions[0].Weight
}

// TestLiveFirstPublishReturnsVersionOne is the other half: the guard must not
// change what a first publish answers.
func TestLiveFirstPublishReturnsVersionOne(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := fmt.Sprintf("azdlive-firstpub-%d", time.Now().UnixNano())
	body, err := normalizeRubricBody(name, []byte(
		`{"dimensions":[{"id":"tone","weight":1,"description":"polite"}]}`))
	require.NoError(t, err)

	created, err := client.CreateEvaluatorVersion(ctx, name, body, nil, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, created.Version, ProjectEndpointAPIVersion)
	})

	require.Equal(t, "1", created.Version,
		"a name the project has never seen must publish as version 1")
}
