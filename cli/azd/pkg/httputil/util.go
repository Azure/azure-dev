package httputil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Reads the raw HTTP response and attempt to convert it into the specified type
// Typically used in conjunction with runtime.WithCaptureResponse(...) to get access to the underlying HTTP response of the
// SDK API call.
func ReadRawResponse[T any](response *http.Response) (*T, error) {
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	instance := new(T)

	err = json.Unmarshal(data, instance)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling JSON from response: %w", err)
	}

	return instance, nil
}
