package appdetect

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

func download(requestUrl string) ([]byte, error) {
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
	resp, err := client.Get(requestUrl)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Println("failed to close http response body")
		}
	}(resp.Body)
	return io.ReadAll(resp.Body)
}

func isAllowedHost(host string) bool {
	return host == "repo.maven.apache.org"
}
