package recording

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

// httpPollDiscarder discards awaiting-done polling interactions from the cassette.
// As a result of this, the cassette will only contain the final result of the polling operation.
// When playing back interactions from the cassette, this results in fast-forwarding behavior.
//
// The type of polling protocols httpPollDiscarder detects includes:
// - Azure specific async polling protocols (implementation closely matches ones supported by azure-sdk-for-go)
// - Idiomatic HTTP async polling protocols (Location header, 201,202 status codes)
type httpPollDiscarder struct {
	pollInProgress poller
}

func (d *httpPollDiscarder) BeforeSave(i *cassette.Interaction) error {
	if d.pollInProgress != nil && d.pollInProgress.Done(i) {
		// Polling is done, reset pollInProgress
		d.pollInProgress = nil
	}

	// Discard awaiting-done polling interactions
	if d.pollInProgress != nil {
		i.DiscardOnSave = true
		return nil
	}

	// Check if the interaction is a polling request.
	// If so, set the current poll in progress.
	if op, err := NewPoller(i); op != nil {
		if err != nil {
			return err
		}
		d.pollInProgress = op
	}

	return nil
}

func NewPoller(i *cassette.Interaction) (poller, error) {
	// accepted status codes for polling operations
	if c := i.Response.Code; c != http.StatusOK &&
		c != http.StatusAccepted &&
		c != http.StatusCreated &&
		c != http.StatusNoContent {
		return nil, nil
	}

	// order here matches the order in azruntime.Poller
	if isAsyncPoll(i) {
		return newAsyncPoll(i)
	}

	if isOpPoll(i) {
		return newOpPoll(i)
	}

	if isLocationPoll(i) {
		return newLocationPoll(i)
	}

	if isBodyPoll(i) {
		return newBodyPoll(i)
	}

	return nil, nil
}

type poller interface {
	Done(i *cassette.Interaction) bool
}

// Poller that uses Azure-AsyncOperation header.
type asyncPoller struct {
	location url.URL
}

func newAsyncPoll(i *cassette.Interaction) (*asyncPoller, error) {
	url, err := parseLocationUrl(i.Response.Headers.Get("Azure-AsyncOperation"))
	if err != nil {
		return nil, err
	}
	return &asyncPoller{
		location: *url,
	}, nil
}

func (a *asyncPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, a.location) {
		return false
	}

	status, err := GetStatus(i.Response.Body)
	if err != nil {
		panic(err)
	}

	if status == "" {
		panic("asyncPoller: the response did not contain a status")
	}

	return isTerminalState(status)
}

// Poller that uses Operation-Location header.
type opPoller struct {
	location url.URL
}

func isOpPoll(i *cassette.Interaction) bool {
	return i.Response.Headers.Get("Operation-Location") != ""
}

func newOpPoll(i *cassette.Interaction) (*opPoller, error) {
	url, err := parseLocationUrl(i.Response.Headers.Get("Operation-Location"))
	if err != nil {
		return nil, err
	}
	return &opPoller{
		location: *url,
	}, nil
}

func (o *opPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, o.location) {
		return false
	}

	status, err := GetStatus(i.Response.Body)
	if err != nil {
		panic(err)
	}

	if status == "" {
		panic("opPoller: the response did not contain a status")
	}

	return isTerminalState(status)
}

// Poller that uses Location header.
//
// By default, this is a poller that checks for termination when HTTP status code is not 202.
// In cases where the Azure-specific provisioning state is present in the body, that is used instead
type locPoller struct {
	location url.URL
}

// isLocationPoll verifies the response must have status code 202 with Location header set
func isLocationPoll(i *cassette.Interaction) bool {
	return i.Response.Code == http.StatusAccepted && i.Response.Headers.Get("Location") != ""
}

func newLocationPoll(i *cassette.Interaction) (*locPoller, error) {
	url, err := parseLocationUrl(i.Response.Headers.Get("Location"))
	if err != nil {
		return nil, err
	}
	return &locPoller{
		location: *url,
	}, nil
}

func (l *locPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, l.location) {
		return false
	}

	// location polling can return an updated polling URL
	if h := i.Response.Headers.Get("Location"); h != "" {
		url, err := url.Parse(i.Response.Headers.Get("Location"))
		if err != nil {
			panic(err)
		}

		l.location = *url
	}

	// if provisioning state is available, use that. this is only
	// for some ARM LRO scenarios (e.g. DELETE with a Location header)
	provState, _ := GetProvisioningState(i.Response.Body)
	if provState != "" {
		return isTerminalState(provState)
	}

	return i.Response.Code != http.StatusAccepted
}

// Poller for resource creation polling.
//
// The resource is created with a PUT or PATCH request with a response code of 201.
// The client awaits a 202 or 204 response for the requested resource.
// Azure also returns a response with a "state" in the body, which is used as a fallback.
type bodyPoller struct {
	location url.URL
}

// the well-known set of LRO status/provisioning state values.
const (
	statusSucceeded  = "Succeeded"
	statusCanceled   = "Canceled"
	statusFailed     = "Failed"
	statusInProgress = "InProgress"
)

func isBodyPoll(i *cassette.Interaction) bool {
	return (i.Request.Method == http.MethodPatch ||
		i.Request.Method == http.MethodPut) &&
		i.Response.Code == http.StatusCreated
}

func newBodyPoll(i *cassette.Interaction) (*bodyPoller, error) {
	loc, err := parseLocationUrl(i.Request.URL)
	if err != nil {
		return nil, err
	}
	return &bodyPoller{
		location: *loc,
	}, nil
}

func (b *bodyPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, b.location) {
		return false
	}

	state, err := GetProvisioningState(i.Response.Body)
	if err != nil && !errors.Is(err, errNoBody) {
		panic(err)
	}

	if i.Response.Code == http.StatusCreated && state != "" {
		// absence of provisioning state is ok for a 201, means the operation is in progress
		return false
	} else if i.Response.Code == http.StatusOK && state == "" {
		return true
	} else if i.Response.Code == http.StatusNoContent {
		return true
	}

	return isTerminalState(state)
}

var errNoBody = errors.New("response did not contain a body")

func jsonBody(respBody string) (map[string]any, error) {
	if len(respBody) == 0 {
		return nil, errNoBody
	}

	var jsonBody map[string]any
	if err := json.Unmarshal([]byte(respBody), &jsonBody); err != nil {
		return nil, err
	}
	return jsonBody, nil
}

func GetProvisioningState(respBody string) (string, error) {
	jsonBody, err := jsonBody(respBody)
	if err != nil {
		return "", err
	}
	return provisioningState(jsonBody), nil
}

// GetStatus returns the LRO's status from the response body.
// Typically used for Azure-AsyncOperation flows.
// If there is no status in the response body the empty string is returned.
func GetStatus(respBody string) (string, error) {
	jsonBody, err := jsonBody(respBody)
	if err != nil {
		return "", err
	}
	return status(jsonBody), nil
}

// provisioningState returns the provisioning state from the response or the empty string.
func provisioningState(jsonBody map[string]any) string {
	jsonProps, ok := jsonBody["properties"]
	if !ok {
		return ""
	}
	props, ok := jsonProps.(map[string]any)
	if !ok {
		return ""
	}
	rawPs, ok := props["provisioningState"]
	if !ok {
		return ""
	}
	ps, ok := rawPs.(string)
	if !ok {
		return ""
	}
	return ps
}

func status(jsonBody map[string]any) string {
	rawStatus, ok := jsonBody["status"]
	if !ok {
		return ""
	}
	status, ok := rawStatus.(string)
	if !ok {
		return ""
	}
	return status
}

func isTerminalState(status string) bool {
	return status == statusSucceeded || status == statusFailed || status == statusCanceled
}

func parseLocationUrl(loc string) (*url.URL, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() {
		return nil, errors.New("location must be an absolute URL")
	}

	return u, nil
}

func isAsyncPoll(i *cassette.Interaction) bool {
	return i.Response.Headers.Get("Azure-AsyncOperation") != ""
}

// urlMatch returns true if the two URLs are equal, ignoring the port portion of Host
// (which is generally present when proxying through https connect).
func urlMatch(reqUrl string, loc url.URL) bool {
	req, err := url.Parse(reqUrl)
	if err != nil {
		panic(err)
	}

	// remove port from host
	req.Host = trimPort(req.Host)
	loc.Host = trimPort(loc.Host)

	return req.String() == loc.String()
}

func trimPort(host string) string {
	if i := strings.LastIndex(host, ":"); i != -1 {
		return host[:i]
	}
	return host
}
