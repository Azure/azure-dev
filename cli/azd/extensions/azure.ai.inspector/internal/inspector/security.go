// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func validateAgentProxyURL(rawURL string, agentPort int) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("proxy URL scheme %q is not allowed", target.Scheme)
	}
	if target.User != nil {
		return nil, fmt.Errorf("proxy URL must not include user information")
	}
	if !isAllowedLocalHost(target.Hostname()) {
		return nil, fmt.Errorf("proxy URL host %q is not allowed", target.Hostname())
	}

	port, err := strconv.Atoi(target.Port())
	if err != nil || port != agentPort {
		return nil, fmt.Errorf("proxy URL port must be %d", agentPort)
	}

	return target, nil
}

func validateExternalBrowserURL(rawURL string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse browser URL: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("browser URL scheme %q is not allowed", target.Scheme)
	}
	if target.User != nil {
		return nil, fmt.Errorf("browser URL must not include user information")
	}
	if target.Hostname() == "" {
		return nil, fmt.Errorf("browser URL must include a host")
	}

	return target, nil
}

func isAllowedInspectorHostPort(hostPort string, inspectorPort int) bool {
	host, portValue, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port != inspectorPort {
		return false
	}

	return isAllowedLocalHost(host)
}

func isAllowedInspectorOrigin(origin string, inspectorPort int) bool {
	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https" {
		return false
	}
	if parsedOrigin.User != nil {
		return false
	}
	if parsedOrigin.Path != "" || parsedOrigin.RawQuery != "" || parsedOrigin.Fragment != "" {
		return false
	}

	return isAllowedInspectorHostPort(parsedOrigin.Host, inspectorPort)
}

func isAllowedLocalHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}
