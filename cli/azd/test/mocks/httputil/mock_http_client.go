package httputil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type MockHttpClient struct {
	expressions []*HttpExpression
}

type HttpExpression struct {
	http        *MockHttpClient
	predicateFn RequestPredicate
	response    *http.Response
	responseFn  RespondFn
	error       error
}

type RequestPredicate func(request *http.Request) bool
type RespondFn func(request *http.Request) (*http.Response, error)

func NewMockHttpUtil() *MockHttpClient {
	return &MockHttpClient{
		expressions: []*HttpExpression{},
	}
}

func (c *MockHttpClient) Do(req *http.Request) (*http.Response, error) {
	var match *HttpExpression

	for i := len(c.expressions) - 1; i >= 0; i-- {
		if c.expressions[i].predicateFn(req) {
			match = c.expressions[i]
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for request: '%s %s'", req.Method, req.URL))
	}

	// If the response function has been set, return the value
	if match.responseFn != nil {
		return match.responseFn(req)
	}

	return match.response, match.error
}

func (c *MockHttpClient) When(predicate RequestPredicate) *HttpExpression {
	expr := HttpExpression{
		http:        c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

func (c *MockHttpClient) Reset() {
	c.expressions = []*HttpExpression{}
}

func (e *HttpExpression) Respond(response *http.Response) *MockHttpClient {
	e.response = response
	return e.http
}

func (e *HttpExpression) RespondFn(responseFn RespondFn) *MockHttpClient {
	e.responseFn = responseFn
	return e.http
}

func (e *HttpExpression) SetError(err error) *MockHttpClient {
	e.error = err
	return e.http
}

func (c *MockHttpClient) AddAzResourceListMock(matchResourceGroupName *string, result any) {
	c.When(func(request *http.Request) bool {
		isMatch := strings.Contains(request.URL.Path, "/resources")
		if matchResourceGroupName != nil {
			isMatch = isMatch && strings.Contains(request.URL.Path, fmt.Sprintf("/resourceGroups/%s/resources", *matchResourceGroupName))
		}

		return isMatch
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		jsonBytes, err := json.Marshal(result)
		if err != nil {
			panic(err)
		}

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})
}

func (c *MockHttpClient) AddDefaultMocks() {
	// This is harmless but should be removed long-term.
	// By default, mock returning an empty list of azure resources instead of crashing.
	// This is an unfortunate mock required due to the side-effect of
	// running "az resource list" as part of loading a project in project.GetProject.
	emptyResult := armresources.ResourceListResult{
		Value: []*armresources.GenericResourceExpanded{},
	}

	c.AddAzResourceListMock(nil, emptyResult)
}
