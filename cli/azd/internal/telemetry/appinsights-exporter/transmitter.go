package appinsightsexporter

// Forked implementation from github.com/microsoft/ApplicationInsights-Go

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"sort"
	"time"
)

type Transmitter interface {
	Transmit(payload []byte, items TelemetryItems) (*TransmissionResult, error)
}

type httpTransmitter struct {
	endpoint string
	client   *http.Client
}

type TransmissionResult struct {
	statusCode int
	retryAfter *time.Time
	response   *backendResponse
}

// Structures returned by data collector
type backendResponse struct {
	ItemsReceived int                     `json:"itemsReceived"`
	ItemsAccepted int                     `json:"itemsAccepted"`
	Errors        itemTransmissionResults `json:"errors"`
}

// This needs to be its own type because it implements sort.Interface
type itemTransmissionResults []*itemTransmissionResult

type itemTransmissionResult struct {
	Index      int    `json:"index"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

const (
	successResponse                         = 200
	partialSuccessResponse                  = 206
	requestTimeoutResponse                  = 408
	tooManyRequestsResponse                 = 429
	tooManyRequestsOverExtendedTimeResponse = 439
	errorResponse                           = 500
	serviceUnavailableResponse              = 503
)

func NewTransmitter(endpointAddress string, client *http.Client) Transmitter {
	if client == nil {
		client = http.DefaultClient
	}
	return &httpTransmitter{endpointAddress, client}
}

func (transmitter *httpTransmitter) Transmit(payload []byte, items TelemetryItems) (*TransmissionResult, error) {
	// Compress the payload
	var postBody bytes.Buffer
	gzipWriter := gzip.NewWriter(&postBody)
	if _, err := gzipWriter.Write(payload); err != nil {
		diagLog.Printf("Failed to compress the payload: %s", err.Error())
		gzipWriter.Close()
		return nil, err
	}

	gzipWriter.Close()

	req, err := http.NewRequest(http.MethodPost, transmitter.endpoint, &postBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/x-json-stream")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := transmitter.client.Do(req)
	if err != nil {
		diagLog.Printf("Failed to transmit telemetry: %s", err.Error())
		return nil, err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		diagLog.Printf("Failed to read response from server: %s", err.Error())
		return nil, err
	}

	result := &TransmissionResult{statusCode: resp.StatusCode}

	// Grab Retry-After header
	if retryAfterValue, ok := resp.Header[http.CanonicalHeaderKey("Retry-After")]; ok && len(retryAfterValue) == 1 {
		if retryAfterTime, err := time.Parse(time.RFC1123, retryAfterValue[0]); err == nil {
			result.retryAfter = &retryAfterTime
		}
	}

	// Parse body, if possible
	response := &backendResponse{}
	if err := json.Unmarshal(body, &response); err == nil {
		result.response = response
	}

	// Write diagnostics
	if result.response != nil {
		if len(result.response.Errors) > 0 {
			diagLog.Printf("Errors:")
			for _, err := range result.response.Errors {
				if err.Index < len(items) {
					diagLog.Printf("#%d - %d %s", err.Index, err.StatusCode, err.Message)
					diagLog.Printf("Telemetry item:\n\t%s", string(items[err.Index:err.Index+1].Serialize()))
				}
			}
		} else {
			diagLog.Printf("Succeeded in sending %d items.", len(items))
		}
	}

	return result, nil
}

func (result *TransmissionResult) IsSuccess() bool {
	return result.statusCode == successResponse ||
		// Partial response but all items accepted
		(result.statusCode == partialSuccessResponse &&
			result.response != nil &&
			result.response.ItemsReceived == result.response.ItemsAccepted)
}

func (result *TransmissionResult) IsFailure() bool {
	return result.statusCode != successResponse && result.statusCode != partialSuccessResponse
}

func (result *TransmissionResult) CanRetry() bool {
	if result.IsSuccess() {
		return false
	}

	return result.statusCode == partialSuccessResponse ||
		result.retryAfter != nil ||
		(result.statusCode == requestTimeoutResponse ||
			result.statusCode == serviceUnavailableResponse ||
			result.statusCode == errorResponse ||
			result.statusCode == tooManyRequestsResponse ||
			result.statusCode == tooManyRequestsOverExtendedTimeResponse)
}

func (result *TransmissionResult) IsPartialSuccess() bool {
	return result.statusCode == partialSuccessResponse &&
		result.response != nil &&
		result.response.ItemsReceived != result.response.ItemsAccepted
}

func (result *TransmissionResult) IsThrottled() bool {
	return result.statusCode == tooManyRequestsResponse ||
		result.statusCode == tooManyRequestsOverExtendedTimeResponse ||
		result.retryAfter != nil
}

func (result *itemTransmissionResult) CanRetry() bool {
	return result.StatusCode == requestTimeoutResponse ||
		result.StatusCode == serviceUnavailableResponse ||
		result.StatusCode == errorResponse ||
		result.StatusCode == tooManyRequestsResponse ||
		result.StatusCode == tooManyRequestsOverExtendedTimeResponse
}

func (result *TransmissionResult) RetryAfter() *time.Time {
	return result.retryAfter
}

func (result *TransmissionResult) StatusCode() int {
	return result.statusCode
}

func (result *TransmissionResult) GetRetryItems(payload []byte, items TelemetryItems) ([]byte, TelemetryItems) {
	if result.statusCode == partialSuccessResponse && result.response != nil {
		// Make sure errors are ordered by index
		sort.Sort(result.response.Errors)

		var resultPayload bytes.Buffer
		resultItems := make(TelemetryItems, 0)
		ptr := 0
		idx := 0

		// Find each retryable error
		for _, responseResult := range result.response.Errors {
			if responseResult.CanRetry() {
				// Advance ptr to start of desired line
				for ; idx < responseResult.Index && ptr < len(payload); ptr++ {
					if payload[ptr] == '\n' {
						idx++
					}
				}

				startPtr := ptr
				// Read to end of line
				for ; idx == responseResult.Index && ptr < len(payload); ptr++ {
					if payload[ptr] == '\n' {
						idx++
					}
				}

				// Copy item into output buffer
				resultPayload.Write(payload[startPtr:ptr])
				resultItems = append(resultItems, items[responseResult.Index])
			}
		}

		return resultPayload.Bytes(), resultItems
	} else if result.CanRetry() {
		return payload, items
	} else {
		return payload[:0], items[:0]
	}
}

// sort.Interface implementation for Errors[] list

func (results itemTransmissionResults) Len() int {
	return len(results)
}

func (results itemTransmissionResults) Less(i, j int) bool {
	return results[i].Index < results[j].Index
}

func (results itemTransmissionResults) Swap(i, j int) {
	tmp := results[i]
	results[i] = results[j]
	results[j] = tmp
}
