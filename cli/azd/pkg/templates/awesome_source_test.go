package templates

import (
	"context"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

var testAwesomeAzdTemplates []*awesomeAzdTemplate = []*awesomeAzdTemplate{
	{
		Title:       "template1",
		Description: "Description of template 1",
		Source:      "http://github.com/user/template1",
	},
	{
		Title:       "template2",
		Description: "Description of template 2",
		Source:      "htdtp://github.com/user/template2",
	},
}

func Test_NewAwesomeAzdTemplateSource_ValidUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockAwesomeAzdTemplateSource(mockContext)

	name := "test"
	url := "https://aka.ms/awesome-azd/templates.json"

	source, err := NewAwesomeAzdTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, err)

	require.Equal(t, name, source.Name())
}

func Test_NewAwesomeAzdTemplateSource_ValidUrl_InvalidJson(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	name := "test"
	url := "https://example.com/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, "invalid json")
	})

	source, err := NewAwesomeAzdTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, source)
	require.Error(t, err)
}

func Test_NewAwesomeAzdTemplateSource_InvalidUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	name := "test"
	url := "https://example.com/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	source, err := NewAwesomeAzdTemplateSource(context.Background(), name, url, mockContext.HttpClient)
	require.Nil(t, source)
	require.Error(t, err)
}

func mockAwesomeAzdTemplateSource(mockContext *mocks.MockContext) {
	const url = "https://aka.ms/awesome-azd/templates.json"

	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && req.URL.String() == url
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, testAwesomeAzdTemplates)
	})
}
