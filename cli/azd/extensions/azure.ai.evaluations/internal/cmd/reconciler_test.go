// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The service enriches a definition when it stores it: a rubric of nothing but
// type and dimensions comes back carrying data_schema, init_parameters and
// metrics. Comparing whole documents therefore never matched, and every deploy
// published a redundant version.
func TestSameDefinitionIgnoresServerAddedFields(t *testing.T) {
	authored := []byte(`{
		"name": "r",
		"definition": {
			"type": "rubric",
			"dimensions": [{"id":"accuracy","description":"Correct.","weight":5}]
		}
	}`)

	onService := []byte(`{
		"name": "r",
		"version": "2",
		"created_at": "2026-07-28T00:00:00Z",
		"definition": {
			"type": "rubric",
			"dimensions": [{"id":"accuracy","description":"Correct.","weight":5}],
			"data_schema": {"type":"object","properties":{"query":{"type":"string"}}},
			"init_parameters": {"required":["model"],"properties":{"model":{"type":"string"}}},
			"metrics": {"score":{"type":"number"}}
		}
	}`)

	require.True(t, sameDefinition(onService, authored),
		"server-added fields must not count as a change")
}

// A real edit still registers.
func TestSameDefinitionDetectsAuthoredChange(t *testing.T) {
	authored := []byte(`{"definition":{"type":"rubric","dimensions":[{"id":"a","weight":7}]}}`)
	onService := []byte(`{"definition":{"type":"rubric","dimensions":[{"id":"a","weight":5}],"metrics":{}}}`)

	require.False(t, sameDefinition(onService, authored))
}

// Key order and whitespace are not changes.
func TestSameDefinitionIsStructural(t *testing.T) {
	authored := []byte(`{"definition":{"type":"rubric","dimensions":[{"id":"a","weight":5}]}}`)
	onService := []byte("{\"definition\":{\n  \"dimensions\": [ {\"weight\":5,\"id\":\"a\"} ],\n  \"type\":\"rubric\"\n}}")

	require.True(t, sameDefinition(onService, authored))
}

func TestSameDefinitionRejectsMalformed(t *testing.T) {
	good := []byte(`{"definition":{"type":"rubric"}}`)
	require.False(t, sameDefinition([]byte(`not json`), good))
	require.False(t, sameDefinition(good, []byte(`not json`)))
	require.False(t, sameDefinition([]byte(`{"no":"definition"}`), good))
}
