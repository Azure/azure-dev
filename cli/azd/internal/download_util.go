package internal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

func Download(requestUrl string) ([]byte, error) {
	parsedUrl, err := url.ParseRequestURI(requestUrl)
	if err != nil {
		return nil, err
	}
	if !isAllowedHost(parsedUrl.Host) {
		return nil, fmt.Errorf("invalid host")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
	slog.DebugContext(context.TODO(), "Downloading file.", "requestUrl", requestUrl, "err", err)
	resp, err := client.Get(requestUrl)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			slog.DebugContext(context.TODO(), "Failed to close http body.", "requestUrl", requestUrl, "err", err)
		}
	}(resp.Body)
	return io.ReadAll(resp.Body)
}

func isAllowedHost(host string) bool {
	return host == "repo.maven.apache.org"
}
