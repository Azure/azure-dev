// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"errors"
	"testing"

	"azure.ai.training/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- isARMResourceID ---

func TestIsARMResourceID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain name", "my-cluster", false},
		{"relative path", "./code", false},
		{"absolute non-ARM path", "/etc/hosts", false},
		{"ARM ID lowercase", "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws/computes/cpu", true},
		{"ARM ID mixed case prefix", "/Subscriptions/abc/resourceGroups/rg", true},
		{"ARM ID uppercase prefix", "/SUBSCRIPTIONS/abc", true},
		{"missing leading slash", "subscriptions/abc", false},
		{"only prefix", "/subscriptions/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isARMResourceID(tc.in))
		})
	}
}

// --- Fake resolvers ---

type fakeCompute struct {
	calls     int
	arm       string
	err       error
	lastInput string
}

func (f *fakeCompute) ResolveCompute(_ context.Context, name string) (string, error) {
	f.calls++
	f.lastInput = name
	return f.arm, f.err
}

type fakeCode struct {
	calls     int
	id        string
	err       error
	lastInput string
}

func (f *fakeCode) ResolveCode(_ context.Context, path string) (string, error) {
	f.calls++
	f.lastInput = path
	return f.id, f.err
}

type fakeInput struct {
	calls    int
	uri      string
	err      error
	lastName string
	lastPath string
	lastType string
}

func (f *fakeInput) ResolveInput(_ context.Context, name, path, t string) (string, error) {
	f.calls++
	f.lastName = name
	f.lastPath = path
	f.lastType = t
	return f.uri, f.err
}

// --- ResolveJobDefinition ---

func TestResolveJobDefinition_PassesThroughARMComputeID(t *testing.T) {
	armID := "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws/computes/cpu"
	c := &fakeCompute{arm: "should-not-be-used"}
	r := NewJobResolver(c, &fakeCode{}, &fakeInput{})

	jd := &utils.JobDefinition{Compute: armID}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, armID, jd.Compute, "ARM ID should be passed through unchanged")
	assert.Equal(t, 0, c.calls, "compute resolver must not be called for ARM IDs")
}

func TestResolveJobDefinition_ResolvesPlainComputeName(t *testing.T) {
	resolved := "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws/computes/cpu"
	c := &fakeCompute{arm: resolved}
	r := NewJobResolver(c, &fakeCode{}, &fakeInput{})

	jd := &utils.JobDefinition{Compute: "cpu"}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, resolved, jd.Compute)
	assert.Equal(t, 1, c.calls)
	assert.Equal(t, "cpu", c.lastInput)
}

func TestResolveJobDefinition_ComputeResolveErrorPropagates(t *testing.T) {
	c := &fakeCompute{err: errors.New("boom")}
	r := NewJobResolver(c, &fakeCode{}, &fakeInput{})

	jd := &utils.JobDefinition{Compute: "cpu"}
	err := r.ResolveJobDefinition(context.Background(), jd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cpu")
	assert.Contains(t, err.Error(), "boom")
}

func TestResolveJobDefinition_EmptyComputeSkipsResolver(t *testing.T) {
	c := &fakeCompute{}
	r := NewJobResolver(c, &fakeCode{}, &fakeInput{})

	jd := &utils.JobDefinition{}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))
	assert.Equal(t, 0, c.calls)
}

func TestResolveJobDefinition_PassesThroughRemoteCodeURI(t *testing.T) {
	code := &fakeCode{id: "should-not-be-used"}
	r := NewJobResolver(&fakeCompute{}, code, &fakeInput{})

	cases := []string{
		"azureml://something",
		"https://example.com/code.tar.gz",
		"http://example.com/code.tar.gz",
		"git://github.com/example/repo",
		"git+https://github.com/example/repo",
	}
	for _, uri := range cases {
		t.Run(uri, func(t *testing.T) {
			code.calls = 0
			jd := &utils.JobDefinition{Code: uri}
			require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))
			assert.Equal(t, uri, jd.Code, "remote code URI should be passed through unchanged")
			assert.Equal(t, 0, code.calls, "code resolver must not be called for remote URIs")
		})
	}
}

func TestResolveJobDefinition_ResolvesLocalCodePath(t *testing.T) {
	code := &fakeCode{id: "azureml://datastores/x/paths/y"}
	r := NewJobResolver(&fakeCompute{}, code, &fakeInput{})

	jd := &utils.JobDefinition{Code: "./src"}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, "azureml://datastores/x/paths/y", jd.Code)
	assert.Equal(t, 1, code.calls)
	assert.Equal(t, "./src", code.lastInput)
}

func TestResolveJobDefinition_CodeResolveErrorPropagates(t *testing.T) {
	code := &fakeCode{err: errors.New("upload failed")}
	r := NewJobResolver(&fakeCompute{}, code, &fakeInput{})

	jd := &utils.JobDefinition{Code: "./src"}
	err := r.ResolveJobDefinition(context.Background(), jd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "./src")
	assert.Contains(t, err.Error(), "upload failed")
}

func TestResolveJobDefinition_LiteralInputValueSkipsResolver(t *testing.T) {
	input := &fakeInput{uri: "should-not-be-used"}
	r := NewJobResolver(&fakeCompute{}, &fakeCode{}, input)

	jd := &utils.JobDefinition{
		Inputs: map[string]utils.InputDefinition{
			"alpha": {Type: "string", Value: "literal-value", Path: "./should-be-ignored"},
		},
	}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, "./should-be-ignored", jd.Inputs["alpha"].Path,
		"literal input.Value bypass: Path must remain unchanged")
	assert.Equal(t, 0, input.calls, "input resolver must not be called when Value is set")
}

func TestResolveJobDefinition_PassesThroughRemoteInputPath(t *testing.T) {
	input := &fakeInput{uri: "should-not-be-used"}
	r := NewJobResolver(&fakeCompute{}, &fakeCode{}, input)

	jd := &utils.JobDefinition{
		Inputs: map[string]utils.InputDefinition{
			"data": {Type: "uri_folder", Path: "azureml://datastores/x/paths/y"},
		},
	}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, "azureml://datastores/x/paths/y", jd.Inputs["data"].Path)
	assert.Equal(t, 0, input.calls)
}

func TestResolveJobDefinition_ResolvesLocalInputPath(t *testing.T) {
	input := &fakeInput{uri: "azureml://datastores/x/paths/data"}
	r := NewJobResolver(&fakeCompute{}, &fakeCode{}, input)

	jd := &utils.JobDefinition{
		Inputs: map[string]utils.InputDefinition{
			"training": {Type: "uri_folder", Path: "./data"},
		},
	}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, "azureml://datastores/x/paths/data", jd.Inputs["training"].Path)
	assert.Equal(t, 1, input.calls)
	assert.Equal(t, "training", input.lastName)
	assert.Equal(t, "./data", input.lastPath)
	assert.Equal(t, "uri_folder", input.lastType)
}

func TestResolveJobDefinition_InputResolveErrorPropagates(t *testing.T) {
	input := &fakeInput{err: errors.New("dataset upload failed")}
	r := NewJobResolver(&fakeCompute{}, &fakeCode{}, input)

	jd := &utils.JobDefinition{
		Inputs: map[string]utils.InputDefinition{
			"training": {Type: "uri_folder", Path: "./data"},
		},
	}
	err := r.ResolveJobDefinition(context.Background(), jd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "training")
	assert.Contains(t, err.Error(), "./data")
	assert.Contains(t, err.Error(), "dataset upload failed")
}

func TestResolveJobDefinition_ResolvesAllSections(t *testing.T) {
	compute := &fakeCompute{arm: "/subscriptions/abc/computes/cpu"}
	code := &fakeCode{id: "azureml://code/v1"}
	input := &fakeInput{uri: "azureml://data/v1"}
	r := NewJobResolver(compute, code, input)

	jd := &utils.JobDefinition{
		Compute: "cpu",
		Code:    "./src",
		Inputs: map[string]utils.InputDefinition{
			"training": {Type: "uri_folder", Path: "./data"},
		},
	}
	require.NoError(t, r.ResolveJobDefinition(context.Background(), jd))

	assert.Equal(t, "/subscriptions/abc/computes/cpu", jd.Compute)
	assert.Equal(t, "azureml://code/v1", jd.Code)
	assert.Equal(t, "azureml://data/v1", jd.Inputs["training"].Path)
	assert.Equal(t, 1, compute.calls)
	assert.Equal(t, 1, code.calls)
	assert.Equal(t, 1, input.calls)
}
