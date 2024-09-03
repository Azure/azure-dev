package recording

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_gzip2HttpRoundTripper_ContentLength validates that a response served with gzip encoding is correctly expanded
// and the Content-Length header is updated to reflect the expanded size.
func Test_gzip2HttpRoundTripper_ContentLength(t *testing.T) {
	message := "This content was served via gzip"

	buf := &bytes.Buffer{}
	writer := gzip.NewWriter(buf)

	_, err := writer.Write([]byte(message))
	require.NoError(t, err)
	err = writer.Close()
	require.NoError(t, err)

	rt := &gzip2HttpRoundTripper{
		transport: funcRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				Status:     "200 OK",
				StatusCode: 200,
				Header: http.Header{
					"Content-Encoding": []string{"gzip"},
					"Content-Length":   []string{fmt.Sprintf("%d", buf.Len())},
				},
				Body:          io.NopCloser(bytes.NewReader(buf.Bytes())),
				ContentLength: int64(buf.Len()),
			}, nil
		}),
	}

	resp, err := rt.RoundTrip(httptest.NewRequest("GET", "http://example.com", nil))
	assert.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, message, string(body))
	assert.Equal(t, int64(len(message)), resp.ContentLength)
	assert.Equal(t, fmt.Sprintf("%d", len(message)), resp.Header.Get("Content-Length"))
}

// funcRoundTripper is an http.RoundTripper backed by a function
type funcRoundTripper func(req *http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper
func (f funcRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestBlobClientGetProperties validates that the azure-sdk-for-go block blob's client GetProperties method returns
// a non nil value, when recording is enabled. The SDK relies on the Content-Length header being present in HEAD responses.
func TestBlobClientGetProperties(t *testing.T) {
	msg := "Hello, world."

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(msg)))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(msg))
		require.NoError(t, err)
	}))

	session := Start(t, WithHostMapping(strings.TrimPrefix(server.URL, "http://"), "127.0.0.1:80"))
	proxyClient, err := proxyClient(session.ProxyUrl)
	require.NoError(t, err)

	blobClient, err := blockblob.NewClientWithNoCredential(server.URL+"/test.txt", &blockblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: proxyClient,
		},
	})
	assert.NoError(t, err)

	props, err := blobClient.GetProperties(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, props.ContentLength)
	assert.Equal(t, int64(len(msg)), *props.ContentLength)

	server.Close()
}
