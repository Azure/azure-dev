package httputil

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
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

// TlsEnabledTransport returns a http.Transport that has TLS configured to use the provided
// Base64 DER-encoded certificate. The returned http.Transport inherits defaults from http.DefaultTransport.
func TlsEnabledTransport(derBytes string) (*http.Transport, error) {
	certBytes, decodeErr := base64.StdEncoding.DecodeString(derBytes)
	if decodeErr != nil {
		return nil,
			fmt.Errorf("failed to decode provided server cert: %w", decodeErr)
	}

	cert, certParseErr := x509.ParseCertificate(certBytes)
	if certParseErr != nil {
		return nil,
			fmt.Errorf("failed to parse provided server cert: %w", certParseErr)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(cert)
	tlsConfig := &tls.Config{
		RootCAs:    caCertPool,
		MinVersion: tls.VersionTLS12,
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return transport, nil
}

// RetryAfter returns the retry after duration from the response headers.
// If none exists, a zero value is returned.
// Headers are checked in the following order: retry-after-ms, x-ms-retry-after-ms, retry-after
func RetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	type retryData struct {
		header string
		units  time.Duration

		// custom is used when the regular algorithm failed and is optional.
		// the returned duration is used verbatim (units is not applied).
		custom func(string) time.Duration
	}

	nop := func(string) time.Duration { return 0 }

	// the headers are listed in order of preference
	retries := []retryData{
		{
			header: "retry-after-ms",
			units:  time.Millisecond,
			custom: nop,
		},
		{
			header: "x-ms-retry-after-ms",
			units:  time.Millisecond,
			custom: nop,
		},
		{
			header: "retry-after",
			units:  time.Second,

			// retry-after values are expressed in either number of
			// seconds or an HTTP-date indicating when to try again
			custom: func(ra string) time.Duration {
				t, err := time.Parse(time.RFC1123, ra)
				if err != nil {
					return 0
				}
				return time.Until(t)
			},
		},
	}

	for _, retry := range retries {
		v := resp.Header.Get(retry.header)
		if v == "" {
			continue
		}
		if retryAfter, _ := strconv.Atoi(v); retryAfter > 0 {
			return time.Duration(retryAfter) * retry.units
		} else if d := retry.custom(v); d > 0 {
			return d
		}
	}

	return 0
}
