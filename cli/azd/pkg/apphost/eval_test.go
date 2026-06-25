// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalString(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "simple", src: "a string with no replacements", want: "a string with no replacements"},
		{name: "replacement", src: "{this.one.has.a.replacement}", want: "this.one.has.a.replacement"},
		{name: "complex", src: "this {one} has {many} replacements", want: "this one has many replacements"},
		{name: "escape", src: "this {{one}} is {{escaped}}", want: "this {one} is {escaped}"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := EvalString(c.src, func(s string) (string, error) {
				return s, nil
			})

			assert.NoError(t, err)
			assert.Equal(t, c.want, res)
		})
	}

	errorCases := []struct {
		name string
		src  string
	}{
		{name: "unclosed open", src: "this { is unclosed"},
		{name: "unmatched close", src: "this } is unmatched"},
		{name: "unmatched escaped close", src: "this {}} is unmatched"},
		{name: "unmatched escaped open", src: "this {{} is unmatched"},
	}

	for _, c := range errorCases {
		t.Run(c.name, func(t *testing.T) {
			res, err := EvalString(c.src, func(s string) (string, error) {
				return s, nil
			})

			assert.Error(t, err)
			assert.Equal(t, "", res)
		})
	}

	res, err := EvalString("{this.one.has.a.replacement}", func(s string) (string, error) {
		return "", fmt.Errorf("this should cause evalString to fail")
	})

	assert.Error(t, err)
	assert.Equal(t, "", res)
}

func TestInputParameter_CrossResourceError(t *testing.T) {
	r := &Resource{
		Value:  "{other.inputs.pw}",
		Inputs: map[string]Input{"pw": {}},
	}
	in, err := InputParameter("self", r)
	require.Error(t, err)
	require.Nil(t, in)
	require.Contains(t, err.Error(), "does not use inputs from its own resource")
}

func TestInputParameter_MissingInputError(t *testing.T) {
	r := &Resource{
		Value:  "{self.inputs.pw}",
		Inputs: map[string]Input{},
	}
	in, err := InputParameter("self", r)
	require.Error(t, err)
	require.Nil(t, in)
	require.Contains(t, err.Error(), "does not have input")
}

// Test that EvalString correctly propagates UnrecognizedExpressionError by keeping original text.
func TestEvalString_UnrecognizedKept(t *testing.T) {
	res, err := EvalString("prefix-{unknown.thing}-suffix", func(s string) (string, error) {
		return "", UnrecognizedExpressionError{}
	})
	require.NoError(t, err)
	require.Equal(t, "prefix-{unknown.thing}-suffix", res)
}

func TestInfraGenerator_UnsupportedResourceType(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"x": {Type: "totally.unknown.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported resource type")
}

func TestInfraGenerator_DaprRequiresMetadata(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"d": {Type: "dapr.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "required metadata")
}

func TestInfraGenerator_BicepV0_NoPathNoParent(t *testing.T) {
	g := newInfraGenerator()
	m := &Manifest{Resources: map[string]*Resource{
		"mod": {Type: "azure.bicep.v0"},
	}}
	err := g.LoadManifest(m)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not have a path or a parent")
}
