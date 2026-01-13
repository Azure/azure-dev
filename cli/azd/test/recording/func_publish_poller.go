// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

// funcPublishPoller handles polling for Azure Functions flex consumption publish operations.
// The publish flow is:
// 1. POST to /api/publish returns 202 with deployment ID in body and Location header
// 2. Client polls GET /api/deployments/{id} until status is terminal
type funcPublishPoller struct {
	deploymentLocation url.URL
}

// Terminal status values for publish operations (mirrored from pkg/azsdk without dependency)
const (
	publishStatusCancelled      = -1
	publishStatusFailed         = 3
	publishStatusSuccess        = 4
	publishStatusConflict       = 5
	publishStatusPartialSuccess = 6
)

func isFuncPublishPoll(i *cassette.Interaction) bool {
	if i.Request.Method != http.MethodPost || i.Response.Code != http.StatusAccepted {
		return false
	}

	reqUrl, err := url.Parse(i.Request.URL)
	if err != nil {
		return false
	}

	return strings.HasSuffix(reqUrl.Path, "/api/publish")
}

func newFuncPublishPoll(i *cassette.Interaction) (*funcPublishPoller, error) {
	// Use Location header if available (preferred)
	location := i.Response.Headers.Get("Location")
	if location != "" {
		loc, err := parseLocationUrl(location)
		if err != nil {
			return nil, err
		}
		return &funcPublishPoller{
			deploymentLocation: *loc,
		}, nil
	}

	// Fallback: construct from deployment ID in body
	var deploymentId string
	if err := json.Unmarshal([]byte(i.Response.Body), &deploymentId); err != nil {
		return nil, err
	}

	if deploymentId == "" {
		return nil, nil
	}

	reqUrl, err := url.Parse(i.Request.URL)
	if err != nil {
		return nil, err
	}

	deploymentUrl := &url.URL{
		Scheme: reqUrl.Scheme,
		Host:   trimPort(reqUrl.Host),
		Path:   "/api/deployments/" + deploymentId,
	}

	return &funcPublishPoller{
		deploymentLocation: *deploymentUrl,
	}, nil
}

func (f *funcPublishPoller) Done(i *cassette.Interaction) (bool, error) {
	if !urlMatch(i.Request.URL, f.deploymentLocation) {
		return false, errPollInteractionUnmatched
	}

	// Handle 404 responses - deployment endpoint can return 404 when worker is recycled
	// after completion, treat as not matching to avoid breaking the flow
	if i.Response.Code == http.StatusNotFound {
		return false, errPollInteractionUnmatched
	}

	status, err := getFuncPublishStatus(i.Response.Body)
	if err != nil {
		return false, err
	}

	return isFuncPublishTerminalStatus(status), nil
}

func getFuncPublishStatus(respBody string) (int, error) {
	if len(respBody) == 0 {
		return 0, errNoBody
	}

	var body struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal([]byte(respBody), &body); err != nil {
		return 0, err
	}
	return body.Status, nil
}

func isFuncPublishTerminalStatus(status int) bool {
	switch status {
	case publishStatusCancelled,
		publishStatusFailed,
		publishStatusSuccess,
		publishStatusConflict,
		publishStatusPartialSuccess:
		return true
	default:
		return false
	}
}
