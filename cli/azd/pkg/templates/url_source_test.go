package templates

import (
	"context"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_NewUrlTemplateSource_ValidUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	name := "test"
	url := "https://example.com/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, testTemplates)
	})

	source, err := NewUrlTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, err)

	require.Equal(t, name, source.Name())
}

func Test_NewUrlTemplateSource_ValidUrl_InvalidJson(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	name := "test"
	url := "https://example.com/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, "invalid json")
	})

	source, err := NewUrlTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, source)
	require.Error(t, err)
}

func Test_NewUrlTemplateSource_InvalidUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	name := "test"
	url := "https://example.com/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	source, err := NewUrlTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, source)
	require.Error(t, err)
}
