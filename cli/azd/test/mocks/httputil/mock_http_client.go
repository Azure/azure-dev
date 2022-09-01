package httpUtil

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type MockHttpClient struct {
	expressions []*HttpExpression
}

type HttpExpression struct {
	http        *MockHttpClient
	predicateFn RequestPredicate
	response    httputil.HttpResponseMessage
	responseFn  RespondFn
	error       error
}

type RequestPredicate func(request *httputil.HttpRequestMessage) bool
type RespondFn func(request httputil.HttpRequestMessage) (*httputil.HttpResponseMessage, error)

func NewMockHttpUtil() *MockHttpClient {
	return &MockHttpClient{
		expressions: []*HttpExpression{},
	}
}

func (http *MockHttpClient) Send(req *httputil.HttpRequestMessage) (*httputil.HttpResponseMessage, error) {
	var match *HttpExpression

	for _, expr := range http.expressions {
		if expr.predicateFn(req) {
			match = expr
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for request: '%s %s'", req.Method, req.Url))
	}

	// If the response function has been set, return the value
	if match.responseFn != nil {
		return match.responseFn(*req)
	}

	return &match.response, match.error
}

func (http *MockHttpClient) When(predicate RequestPredicate) *HttpExpression {
	expr := HttpExpression{
		http:        http,
		predicateFn: predicate,
	}

	http.expressions = append(http.expressions, &expr)
	return &expr
}

func (http *MockHttpClient) Reset() {
	http.expressions = []*HttpExpression{}
}

func (e *HttpExpression) Respond(response httputil.HttpResponseMessage) *MockHttpClient {
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

func MockResourceGraphEmptyResources(mock *MockHttpClient) {
	mock.When(func(req *httputil.HttpRequestMessage) bool {
		return req.Method == http.MethodPost && strings.Contains(req.Url, "providers/Microsoft.ResourceGraph/resources")
	}).RespondFn(func(request httputil.HttpRequestMessage) (*httputil.HttpResponseMessage, error) {
		jsonResponse := `{"data": [], "total_records": 0}`

		response := httputil.HttpResponseMessage{
			Status: 200,
			Body:   []byte(jsonResponse),
		}

		return &response, nil
	})
}
