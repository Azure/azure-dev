// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package convert

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ToJsonArray_ValidSlice(t *testing.T) {
	input := []string{"alpha", "bravo", "charlie"}
	result, err := ToJsonArray(input)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "alpha", result[0])
	require.Equal(t, "bravo", result[1])
	require.Equal(t, "charlie", result[2])
}

func Test_ToJsonArray_IntSlice(t *testing.T) {
	input := []int{1, 2, 3}
	result, err := ToJsonArray(input)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, float64(1), result[0])
	require.Equal(t, float64(2), result[1])
	require.Equal(t, float64(3), result[2])
}

func Test_ToJsonArray_StructSlice(t *testing.T) {
	input := []Person{
		{Name: "Alice", Address: "123 Main St"},
		{Name: "Bob", Address: "456 Oak Ave"},
	}
	result, err := ToJsonArray(input)
	require.NoError(t, err)
	require.Len(t, result, 2)

	first, ok := result[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Alice", first["Name"])
}

func Test_ToJsonArray_Nil(t *testing.T) {
	result, err := ToJsonArray(nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func Test_ToJsonArray_EmptySlice(t *testing.T) {
	input := []string{}
	result, err := ToJsonArray(input)
	require.NoError(t, err)
	require.Empty(t, result)
}

func Test_ToJsonArray_NonSlice(t *testing.T) {
	// A non-slice value that marshals to JSON but can't
	// unmarshal into []any
	input := map[string]string{"key": "value"}
	_, err := ToJsonArray(input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to convert")
}

func Test_ToJsonArray_UnmarshalableInput(t *testing.T) {
	// A channel can't be marshalled to JSON
	input := make(chan int)
	_, err := ToJsonArray(input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to convert")
}

func Test_FromHttpResponse_ValidJSON(t *testing.T) {
	body := `{"Name":"Alice","Address":"Wonderland"}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	var person Person
	err := FromHttpResponse(resp, &person)
	require.NoError(t, err)
	require.Equal(t, "Alice", person.Name)
	require.Equal(t, "Wonderland", person.Address)
}

func Test_FromHttpResponse_InvalidJSON(t *testing.T) {
	body := `not-json`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	var person Person
	err := FromHttpResponse(resp, &person)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal")
}

func Test_FromHttpResponse_EmptyBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte(""))),
	}

	var result map[string]any
	err := FromHttpResponse(resp, &result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal")
}

type errReader struct{}

func (e *errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func (e *errReader) Close() error { return nil }

func Test_FromHttpResponse_ReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: 200,
		Body:       &errReader{},
	}

	var result map[string]any
	err := FromHttpResponse(resp, &result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read")
}

func Test_FromHttpResponse_Array(t *testing.T) {
	body := `[{"Name":"A","Address":"1"},{"Name":"B","Address":"2"}]`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}

	var people []Person
	err := FromHttpResponse(resp, &people)
	require.NoError(t, err)
	require.Len(t, people, 2)
	require.Equal(t, "A", people[0].Name)
	require.Equal(t, "B", people[1].Name)
}

func Test_ToMap_Nil(t *testing.T) {
	result, err := ToMap(nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func Test_ToMap_MapInput(t *testing.T) {
	input := map[string]string{"key": "val"}
	result, err := ToMap(input)
	require.NoError(t, err)
	require.Equal(t, "val", result["key"])
}

func Test_ToMap_UnmarshalableInput(t *testing.T) {
	input := make(chan int)
	_, err := ToMap(input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to convert")
}

func Test_ParseDuration_ISO8601Formats(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Seconds", "PT5S", "5s"},
		{"Minutes", "PT10M", "10m0s"},
		{"Hours", "PT2H", "2h0m0s"},
		{"Combined", "PT1H30M", "1h30m0s"},
		{"Fractional", "PT0.5S", "500ms"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDuration(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.expected, d.String())
		})
	}
}

func Test_ParseDuration_AlreadyLowercase(t *testing.T) {
	d, err := ParseDuration("10s")
	require.NoError(t, err)
	require.Equal(t, "10s", d.String())
}

func Test_ParseDuration_Invalid(t *testing.T) {
	_, err := ParseDuration("not-a-duration")
	require.Error(t, err)
}

func Test_ToValueWithDefault_Bool(t *testing.T) {
	trueVal := true
	result := ToValueWithDefault(&trueVal, false)
	require.True(t, result)
}

func Test_ToValueWithDefault_BoolNil(t *testing.T) {
	result := ToValueWithDefault[bool](nil, true)
	require.True(t, result)
}

func Test_ToStringWithDefault_IntPointer(t *testing.T) {
	// An *int is not *string, so should return default
	val := 42
	result := ToStringWithDefault(&val, "fallback")
	require.Equal(t, "fallback", result)
}

func Test_ToStringWithDefault_EmptyStringPointer(t *testing.T) {
	empty := ""
	result := ToStringWithDefault(&empty, "default")
	require.Equal(t, "default", result)
}

func Test_ToMap_NestedStruct(t *testing.T) {
	type Inner struct {
		Value int
	}
	type Outer struct {
		Name  string
		Inner Inner
	}
	input := Outer{Name: "test", Inner: Inner{Value: 42}}
	result, err := ToMap(input)
	require.NoError(t, err)
	require.Equal(t, "test", result["Name"])
	inner, ok := result["Inner"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(42), inner["Value"])
}

func Test_ToJsonArray_NestedSlice(t *testing.T) {
	input := [][]int{{1, 2}, {3, 4}}
	result, err := ToJsonArray(input)
	require.NoError(t, err)
	require.Len(t, result, 2)
	first, ok := result[0].([]any)
	require.True(t, ok)
	require.Len(t, first, 2)
}

func Test_FromHttpResponse_ClosesBody(t *testing.T) {
	closed := false
	body := io.NopCloser(
		bytes.NewReader([]byte(`{"Name":"X"}`)),
	)
	resp := &http.Response{
		StatusCode: 200,
		Body: &trackingCloser{
			ReadCloser: body,
			onClose:    func() { closed = true },
		},
	}

	var p Person
	err := FromHttpResponse(resp, &p)
	require.NoError(t, err)
	require.True(t, closed)
}

type trackingCloser struct {
	io.ReadCloser
	onClose func()
}

func (tc *trackingCloser) Close() error {
	tc.onClose()
	return tc.ReadCloser.Close()
}

func Test_FromHttpResponse_LargePayload(t *testing.T) {
	items := make([]Person, 100)
	for i := range items {
		items[i] = Person{
			Name:    "Person",
			Address: "Address",
		}
	}
	data, err := json.Marshal(items)
	require.NoError(t, err)

	resp := &http.Response{
		StatusCode: 200,
		Body: io.NopCloser(
			bytes.NewReader(data),
		),
	}

	var result []Person
	err = FromHttpResponse(resp, &result)
	require.NoError(t, err)
	require.Len(t, result, 100)
}
