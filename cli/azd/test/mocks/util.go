package mocks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Creates a mocked HTTP response with the specified status code and body
func CreateHttpResponseWithBody[T any](request *http.Request, statusCode int, body T) (*http.Response, error) {
	responseJson, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{},
		Request:    request,
		Body:       io.NopCloser(bytes.NewBuffer(responseJson)),
	}, nil
}

// Creates a mocked HTTP response with the specified status code and no body
func CreateEmptyHttpResponse(request *http.Request, statusCode int) (*http.Response, error) {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{},
		Request:    request,
		Body:       http.NoBody,
	}, nil
}

// ReadHttpBody reads the body of an HTTP request or response and converts it into the specified object
func ReadHttpBody(body io.ReadCloser, v any) error {
	defer body.Close()

	jsonBytes, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonBytes, v)
}
