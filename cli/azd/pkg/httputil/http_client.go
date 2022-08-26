// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package httputil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

type HttpRequestMessage struct {
	Url     string
	Method  string
	Headers map[string]string
	Body    string
}

type HttpResponseMessage struct {
	Headers map[string]string
	Status  int
	Body    []byte
}

type HttpClient interface {
	Send(req *HttpRequestMessage) (*HttpResponseMessage, error)
}

type httpClient struct {
}

func (hu *httpClient) Send(req *HttpRequestMessage) (*HttpResponseMessage, error) {
	requestBytes := []byte(req.Body)
	requestReader := bytes.NewReader(requestBytes)

	request, err := http.NewRequest(req.Method, req.Url, requestReader)
	if err != nil {
		return nil, fmt.Errorf("creating request")
	}

	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Accept", "application/json")

	if req.Headers != nil {
		for k, v := range req.Headers {
			request.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("executing http request")
	}

	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response")
	}

	responseMessage := &HttpResponseMessage{
		Status: response.StatusCode,
		Body:   responseBytes,
	}

	return responseMessage, nil
}

func NewHttpClient() HttpClient {
	return &httpClient{}
}

type contextKey string

const (
	httpClientContextKey contextKey = "httpclient"
)

// GetHttpClient attempts to retrieve a HttpUtil instance from the specified context.
// Will return the context if found or create a new instance
func GetHttpClient(ctx context.Context) HttpClient {
	value := ctx.Value(httpClientContextKey)
	client, ok := value.(HttpClient)

	if !ok {
		return NewHttpClient()
	}

	return client
}

func WithHttpClient(ctx context.Context, httpClient HttpClient) context.Context {
	return context.WithValue(ctx, httpClientContextKey, httpClient)
}
