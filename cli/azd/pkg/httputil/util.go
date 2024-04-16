package httputil

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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
