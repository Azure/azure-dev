package httputil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// RespondJSON responds with a 200 response with the JSON encoding of value as the body of the response.
func (e *HttpExpression) RespondJSON(value any) *MockHttpClient {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	e.response = &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBuffer(body)),
	}

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
