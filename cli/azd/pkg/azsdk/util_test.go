package azsdk

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadRawResponse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expectedResponse := &DeployResponse{
			DeployStatus: DeployStatus{
				Id:         "ID",
				Status:     http.StatusOK,
				StatusText: "Things are OK",
				Message:    "Message",
				Progress:   nil,
				Complete:   true,
				Active:     true,
				LogUrl:     "https://log.url",
				SiteName:   "my-site",
			},
		}

		jsonBytes, err := json.Marshal(expectedResponse)
		require.NoError(t, err)

		httpResponse := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}

		actualResponse, err := ReadRawResponse[DeployResponse](httpResponse)
		require.NoError(t, err)
		require.Equal(t, *expectedResponse, *actualResponse)
	})
}
