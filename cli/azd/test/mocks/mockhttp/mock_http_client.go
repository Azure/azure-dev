package mockhttp

import (
	"fmt"
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

func (c *MockHttpClient) CloseIdleConnections() {
	// No-op
}

func (e *HttpExpression) Respond(response *http.Response) *MockHttpClient {
	e.response = response
	return e.http
}

func (e *HttpExpression) RespondFn(responseFn RespondFn) *MockHttpClient {
	e.responseFn = responseFn
	return e.http
}

func (e *HttpExpression) SetRetriableError(err error) *MockHttpClient {
	// Default transport errors are retriable by azure-sdk-for-go RetryPolicy
	e.error = err
	return e.http
}

func (e *HttpExpression) SetNonRetriableError(err error) *MockHttpClient {
	// Return pointer to satisfy NonRetriable interface
	e.error = &nonRetryableError{err}
	return e.http
}

func HasBearerToken(request *http.Request, bearerToken string) bool {
	authHeader := request.Header["Authorization"]
	return len(authHeader) == 1 && authHeader[0] == fmt.Sprintf("Bearer %s", bearerToken)
}

type nonRetryableError struct {
	error
}

// NonRetriable indicates the request which provoked this error shouldn't be retried.
func (nre *nonRetryableError) NonRetriable() {
	// Satisfies NonRetriable interface in azure-sdk-for-go/sdk/internal/errorinfo/
}
