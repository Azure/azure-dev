package httpUtil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

type HttpRequestMessage struct {
	Url     string
	Method  string
	Headers map[string]string
	Body    string
}

type HttpResponseMessage struct {
	Headers map[string]string
	Status  int
	Body    []byte
}

type HttpUtil interface {
	Send(req *HttpRequestMessage) (*HttpResponseMessage, error)
}

type httpUtil struct {
}

func (hu *httpUtil) Send(req *HttpRequestMessage) (*HttpResponseMessage, error) {
	requestBytes := []byte(req.Body)
	requestReader := bytes.NewReader(requestBytes)

	request, err := http.NewRequest(req.Method, req.Url, requestReader)
	if err != nil {
		return nil, fmt.Errorf("creating request")
	}

	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Accept", "application/json")

	if req.Headers != nil {
		for k, v := range req.Headers {
			request.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("executing http request")
	}

	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response")
	}

	responseMessage := &HttpResponseMessage{
		Status: response.StatusCode,
		Body:   responseBytes,
	}

	return responseMessage, nil
}

func NewHttpUtil() HttpUtil {
	return &httpUtil{}
}

// GetHttpUtilFromContext attempts to retrieve a HttpUtil instance from the specified context.
// Will return the context if found or create a new instance
func GetHttpUtilFromContext(ctx context.Context) HttpUtil {
	value := ctx.Value(environment.HttpUtilContextKey)
	client, ok := value.(HttpUtil)

	if !ok {
		return NewHttpUtil()
	}

	return client
}

func DownloadFile(ctx context.Context, url string, authToken string, target string) error {
	// create the file
	file, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("downloading file: %w", err)
	}

	// create opens the file, so let's make sure to close it on exit
	defer file.Close()

	downloadRequest, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	if authToken != "" {
		downloadRequest.Header.Add("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}

	response, err := http.DefaultClient.Do(downloadRequest)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}

	// make sure to close the I/O to body
	defer response.Body.Close()

	// write from network to target file
	_, err = io.Copy(file, response.Body)
	if err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}
