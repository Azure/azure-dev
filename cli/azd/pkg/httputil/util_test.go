package httputil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type exampleResponse struct {
	A string `json:"a"`
	B string `json:"b"`
	C string `json:"c"`
}

func TestReadRawResponse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expectedResponse := &exampleResponse{
			A: "Apple",
			B: "Banana",
			C: "Carrot",
		}

		jsonBytes, err := json.Marshal(expectedResponse)
		require.NoError(t, err)

		httpResponse := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}

		actualResponse, err := ReadRawResponse[exampleResponse](httpResponse)
		require.NoError(t, err)
		require.Equal(t, *expectedResponse, *actualResponse)
	})
}
