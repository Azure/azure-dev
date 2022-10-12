package appinsightsexporter

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testServer struct {
	server *httptest.Server
	notify chan *testRequest

	responseData    []byte
	responseCode    int
	responseHeaders map[string]string
}

type testRequest struct {
	request *http.Request
	body    []byte
}

func (server *testServer) Close() {
	server.server.Close()
	close(server.notify)
}

func (server *testServer) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)

	hdr := writer.Header()
	for k, v := range server.responseHeaders {
		hdr[k] = []string{v}
	}

	writer.WriteHeader(server.responseCode)
	_, _ = writer.Write(server.responseData)

	server.notify <- &testRequest{
		request: req,
		body:    body,
	}
}

func (server *testServer) waitForRequest(t *testing.T) *testRequest {
	select {
	case req := <-server.notify:
		return req
	case <-time.After(time.Second):
		t.Fatal("Server did not receive request within a second")
		return nil /* not reached */
	}
}

func newTestClientServer() (Transmitter, *testServer) {
	server := &testServer{}
	server.server = httptest.NewServer(server)
	server.notify = make(chan *testRequest, 1)
	server.responseCode = 200
	server.responseData = make([]byte, 0)
	server.responseHeaders = make(map[string]string)

	client := NewTransmitter(fmt.Sprintf("http://%s/v2/track", server.server.Listener.Addr().String()), nil)

	return client, server
}

func newTestTlsClientServer(t *testing.T) (Transmitter, *testServer) {
	server := &testServer{}
	server.server = httptest.NewTLSServer(server)
	server.notify = make(chan *testRequest, 1)
	server.responseCode = 200
	server.responseData = make([]byte, 0)
	server.responseHeaders = make(map[string]string)

	client := NewTransmitter(
		fmt.Sprintf("https://%s/v2/track", server.server.Listener.Addr().String()),
		server.server.Client(),
	)

	return client, server
}

func TestBasicTransitTls(t *testing.T) {
	client, server := newTestTlsClientServer(t)

	doBasicTransmit(client, server, t)
}

func TestBasicTransmit(t *testing.T) {
	client, server := newTestClientServer()

	doBasicTransmit(client, server, t)
}

func doBasicTransmit(client Transmitter, server *testServer, t *testing.T) {
	defer server.Close()

	server.responseData = []byte(`{"itemsReceived":3, "itemsAccepted":5, "errors":[]}`)
	server.responseHeaders["Content-type"] = "application/json"
	result, err := client.Transmit([]byte("foobar"), make(TelemetryItems, 0))
	if err != nil {
		t.Log(err.Error())
	}
	req := server.waitForRequest(t)

	if err != nil {
		t.Errorf("err: %s", err.Error())
	}

	if req.request.Method != "POST" {
		t.Error("request.Method")
	}

	encoding := req.request.Header[http.CanonicalHeaderKey("Content-Encoding")]
	if len(encoding) != 1 || encoding[0] != "gzip" {
		t.Errorf("Content-encoding: %q", encoding)
	}

	// Check for gzip magic number
	if len(req.body) < 2 || req.body[0] != 0x1f || req.body[1] != 0x8b {
		t.Fatal("Missing gzip magic number")
	}

	// Decompress payload
	reader, err := gzip.NewReader(bytes.NewReader(req.body))
	if err != nil {
		t.Fatalf("Couldn't create gzip reader: %s", err.Error())
	}

	body, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("Couldn't read compressed data: %s", err.Error())
	}

	if string(body) != "foobar" {
		t.Error("body")
	}

	contentType := req.request.Header[http.CanonicalHeaderKey("Content-Type")]
	if len(contentType) != 1 || contentType[0] != "application/x-json-stream" {
		t.Errorf("Content-type: %q", contentType)
	}

	if result.StatusCode != 200 {
		t.Error("statusCode")
	}

	if result.RetryAfter != nil {
		t.Error("retryAfter")
	}

	if result.Response == nil {
		t.Fatal("response")
	}

	if result.Response.ItemsReceived != 3 {
		t.Error("ItemsReceived")
	}

	if result.Response.ItemsAccepted != 5 {
		t.Error("ItemsAccepted")
	}

	if len(result.Response.Errors) != 0 {
		t.Error("response.Errors")
	}
}

func TestFailedTransmit(t *testing.T) {
	client, server := newTestClientServer()
	defer server.Close()

	server.responseCode = errorResponse
	server.responseData = []byte(
		`{"itemsReceived":3, "itemsAccepted":0, "errors":[{"index": 2, "statusCode": 500, "message": "Hello"}]}`,
	)
	server.responseHeaders["Content-type"] = "application/json"
	result, err := client.Transmit([]byte("foobar"), make(TelemetryItems, 0))
	server.waitForRequest(t)

	if err != nil {
		t.Errorf("err: %s", err.Error())
	}

	if result.StatusCode != errorResponse {
		t.Error("statusCode")
	}

	if result.RetryAfter != nil {
		t.Error("retryAfter")
	}

	if result.Response == nil {
		t.Fatal("response")
	}

	if result.Response.ItemsReceived != 3 {
		t.Error("ItemsReceived")
	}

	if result.Response.ItemsAccepted != 0 {
		t.Error("ItemsAccepted")
	}

	if len(result.Response.Errors) != 1 {
		t.Fatal("len(Errors)")
	}

	if result.Response.Errors[0].Index != 2 {
		t.Error("Errors[0].index")
	}

	if result.Response.Errors[0].StatusCode != errorResponse {
		t.Error("Errors[0].statusCode")
	}

	if result.Response.Errors[0].Message != "Hello" {
		t.Error("Errors[0].message")
	}
}

func TestThrottledTransmit(t *testing.T) {
	client, server := newTestClientServer()
	defer server.Close()

	server.responseCode = errorResponse
	server.responseData = make([]byte, 0)
	server.responseHeaders["Content-type"] = "application/json"
	server.responseHeaders["retry-after"] = "Wed, 09 Aug 2017 23:43:57 UTC"
	result, err := client.Transmit([]byte("foobar"), make(TelemetryItems, 0))
	server.waitForRequest(t)

	if err != nil {
		t.Errorf("err: %s", err.Error())
	}

	if result.StatusCode != errorResponse {
		t.Error("statusCode")
	}

	if result.Response != nil {
		t.Fatal("response")
	}

	if result.RetryAfter == nil {
		t.Fatal("retryAfter")
	}

	if (*result.RetryAfter).Unix() != 1502322237 {
		t.Error("retryAfter.Unix")
	}
}

type resultProperties struct {
	isSuccess        bool
	isFailure        bool
	canRetry         bool
	isThrottled      bool
	isPartialSuccess bool
	retryableErrors  bool
}

func checkTransmitResult(t *testing.T, result *TransmissionResult, expected *resultProperties) {
	retryAfter := "<nil>"
	if result.RetryAfter != nil {
		retryAfter = (*result.RetryAfter).String()
	}
	response := "<nil>"
	if result.Response != nil {
		response = fmt.Sprintf("%v", *result.Response)
	}
	id := fmt.Sprintf("%d, retryAfter:%s, response:%s", result.StatusCode, retryAfter, response)

	if result.IsSuccess() != expected.isSuccess {
		t.Errorf("Expected IsSuccess() == %t [%s]", expected.isSuccess, id)
	}

	if result.IsFailure() != expected.isFailure {
		t.Errorf("Expected IsFailure() == %t [%s]", expected.isFailure, id)
	}

	if result.CanRetry() != expected.canRetry {
		t.Errorf("Expected CanRetry() == %t [%s]", expected.canRetry, id)
	}

	if result.IsThrottled() != expected.isThrottled {
		t.Errorf("Expected IsThrottled() == %t [%s]", expected.isThrottled, id)
	}

	if result.IsPartialSuccess() != expected.isPartialSuccess {
		t.Errorf("Expected IsPartialSuccess() == %t [%s]", expected.isPartialSuccess, id)
	}

	// retryableErrors is true if CanRetry() and any error is recoverable
	retryableErrors := false
	if result.CanRetry() && result.Response != nil {
		for _, err := range result.Response.Errors {
			if err.CanRetry() {
				retryableErrors = true
			}
		}
	}

	if retryableErrors != expected.retryableErrors {
		t.Errorf("Expected any(Errors.CanRetry) == %t [%s]", expected.retryableErrors, id)
	}
}

func TestTransmitResults(t *testing.T) {
	retryAfter := time.Unix(1502322237, 0)
	partialNoRetries := &BackendResponse{
		ItemsAccepted: 3,
		ItemsReceived: 5,
		Errors: []*ItemTransmissionResult{
			{Index: 2, StatusCode: 400, Message: "Bad 1"},
			{Index: 4, StatusCode: 400, Message: "Bad 2"},
		},
	}

	partialSomeRetries := &BackendResponse{
		ItemsAccepted: 2,
		ItemsReceived: 4,
		Errors: []*ItemTransmissionResult{
			{Index: 2, StatusCode: 400, Message: "Bad 1"},
			{Index: 4, StatusCode: 408, Message: "OK Later"},
		},
	}

	noneAccepted := &BackendResponse{
		ItemsAccepted: 0,
		ItemsReceived: 5,
		Errors: []*ItemTransmissionResult{
			{Index: 0, StatusCode: 500, Message: "Bad 1"},
			{Index: 1, StatusCode: 500, Message: "Bad 2"},
			{Index: 2, StatusCode: 500, Message: "Bad 3"},
			{Index: 3, StatusCode: 500, Message: "Bad 4"},
			{Index: 4, StatusCode: 500, Message: "Bad 5"},
		},
	}

	allAccepted := &BackendResponse{
		ItemsAccepted: 6,
		ItemsReceived: 6,
		Errors:        make([]*ItemTransmissionResult, 0),
	}

	checkTransmitResult(t, &TransmissionResult{200, nil, allAccepted},
		&resultProperties{isSuccess: true})
	checkTransmitResult(t, &TransmissionResult{206, nil, partialSomeRetries},
		&resultProperties{isPartialSuccess: true, canRetry: true, retryableErrors: true})
	checkTransmitResult(t, &TransmissionResult{206, nil, partialNoRetries},
		&resultProperties{isPartialSuccess: true, canRetry: true})
	checkTransmitResult(t, &TransmissionResult{206, nil, noneAccepted},
		&resultProperties{isPartialSuccess: true, canRetry: true, retryableErrors: true})
	checkTransmitResult(t, &TransmissionResult{206, nil, allAccepted},
		&resultProperties{isSuccess: true})
	checkTransmitResult(t, &TransmissionResult{400, nil, nil},
		&resultProperties{isFailure: true})
	checkTransmitResult(t, &TransmissionResult{408, nil, nil},
		&resultProperties{isFailure: true, canRetry: true})
	checkTransmitResult(t, &TransmissionResult{408, &retryAfter, nil},
		&resultProperties{isFailure: true, canRetry: true, isThrottled: true})
	checkTransmitResult(t, &TransmissionResult{429, nil, nil},
		&resultProperties{isFailure: true, canRetry: true, isThrottled: true})
	checkTransmitResult(t, &TransmissionResult{429, &retryAfter, nil},
		&resultProperties{isFailure: true, canRetry: true, isThrottled: true})
	checkTransmitResult(t, &TransmissionResult{500, nil, nil},
		&resultProperties{isFailure: true, canRetry: true})
	checkTransmitResult(t, &TransmissionResult{503, nil, nil},
		&resultProperties{isFailure: true, canRetry: true})
	checkTransmitResult(t, &TransmissionResult{401, nil, nil},
		&resultProperties{isFailure: true})
	checkTransmitResult(t, &TransmissionResult{408, nil, partialSomeRetries},
		&resultProperties{isFailure: true, canRetry: true, retryableErrors: true})
	checkTransmitResult(t, &TransmissionResult{500, nil, partialSomeRetries},
		&resultProperties{isFailure: true, canRetry: true, retryableErrors: true})
}

func TestGetRetryItems(t *testing.T) {
	// Keep a pristine copy.
	originalPayload, originalItems := makePayload()

	res1 := &TransmissionResult{
		StatusCode: 200,
		Response:   &BackendResponse{ItemsReceived: 7, ItemsAccepted: 7},
	}

	payload1, items1 := res1.GetRetryItems(makePayload())
	if len(payload1) > 0 || len(items1) > 0 {
		t.Error("GetRetryItems shouldn't return anything")
	}

	res2 := &TransmissionResult{StatusCode: 408}

	payload2, items2 := res2.GetRetryItems(makePayload())
	if string(originalPayload) != string(payload2) || len(items2) != 7 {
		t.Error("GetRetryItems shouldn't return anything")
	}

	res3 := &TransmissionResult{
		StatusCode: 206,
		Response: &BackendResponse{
			ItemsReceived: 7,
			ItemsAccepted: 4,
			Errors: []*ItemTransmissionResult{
				{Index: 1, StatusCode: 200, Message: "OK"},
				{Index: 3, StatusCode: 400, Message: "Bad"},
				{Index: 5, StatusCode: 408, Message: "Later"},
				{Index: 6, StatusCode: 500, Message: "Oops"},
			},
		},
	}

	payload3, items3 := res3.GetRetryItems(makePayload())
	expected3 := TelemetryItems{originalItems[5], originalItems[6]}
	if string(payload3) != string(expected3.Serialize()) || len(items3) != 2 {
		t.Error("Unexpected result")
	}
}

func makePayload() ([]byte, TelemetryItems) {
	var buffer TelemetryItems
	for i := 0; i < 7; i++ {
		buffer = append(buffer, *SpanToEnvelope(getDefaultSpanStub().Snapshot()))
	}

	return buffer.Serialize(), buffer
}
