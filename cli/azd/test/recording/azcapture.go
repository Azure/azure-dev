package recording

import (
	"encoding/json"
	"net/http"
	"net/url"

	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

type httpPollDiscarder struct {
	pollInProgress poller
}

// "Location"

// Azure-AsyncOperation -> body "status"

// PATCH or PUT
// - Check Status first.
// - Then check body "properties/provisioningState"

//

func pollLocation(i *cassette.Interaction) (*url.URL, error) {
	loc := i.Response.Headers.Get("Operation-Location")
	if loc == "" {
		return nil, nil
	}

	return url.Parse(loc)
}

func (d *httpPollDiscarder) BeforeSave(i *cassette.Interaction) error {
	if d.pollInProgress != nil && d.pollInProgress.Done(i) {
		// Polling is done, reset pollInProgress
		d.pollInProgress = nil
	}

	if d.pollInProgress != nil {
		// Fast forward messages that are simply waiting for polling to complete
		i.DiscardOnSave = true
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
	// order here matches what azruntime.Poller does

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

type asyncPoller struct {
	location url.URL
}

func isAsyncPoll(i *cassette.Interaction) bool {
	return i.Request.Method == "PUT" && i.Response.Headers.Get("Azure-AsyncOperation") != ""
}

func newAsyncPoll(i *cassette.Interaction) (*asyncPoller, error) {
	url, err := url.Parse(i.Response.Headers.Get("Azure-AsyncOperation"))
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

	//!TODO
	return status != statusInProgress
}

type opPoller struct {
	location url.URL
}

func isOpPoll(i *cassette.Interaction) bool {
	return i.Request.Method == "PUT" && i.Response.Headers.Get("Operation-Location") != ""
}

func newOpPoll(i *cassette.Interaction) (*opPoller, error) {
	url, err := url.Parse(i.Response.Headers.Get("Operation-Location"))
	if err != nil {
		return nil, err
	}
	return &opPoller{
		location: *url,
	}, nil
}

func urlMatch(reqUrl string, loc url.URL) bool {
	req, err := url.Parse(reqUrl)
	if err != nil {
		panic(err)
	}

	return req.String() == loc.String()
}

func (o *opPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, o.location) {
		return false
	}

	//!TODO
	return false
}

type locPoller struct {
	location url.URL
}

func isLocationPoll(i *cassette.Interaction) bool {
	return i.Request.Method == "PUT" && i.Response.Headers.Get("Location") != ""
}

func newLocationPoll(i *cassette.Interaction) (*locPoller, error) {
	url, err := url.Parse(i.Response.Headers.Get("Location"))
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

	//!TODO
	return false
}

type bodyPoller struct {
	state    string
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
	return (i.Request.Method == "PATCH" ||
		i.Request.Method == "PUT") &&
		i.Response.Code == http.StatusCreated
}

func newBodyPoll(i *cassette.Interaction) (*bodyPoller, error) {
	body, _ := jsonBody(i.Response.Body)
	state := provisioningState(body)
	if state == "" {
		state = statusInProgress
	}

	loc, err := url.Parse(i.Request.URL)
	if err != nil {
		return nil, err
	}
	return &bodyPoller{
		state:    state,
		location: *loc,
	}, nil
}

func (b *bodyPoller) Done(i *cassette.Interaction) bool {
	if !urlMatch(i.Request.URL, b.location) {
		return false
	}

	if i.Response.Code != http.StatusAccepted {
		return true
	}

	body, err := jsonBody(i.Response.Body)
	if err != nil {
		panic(err)
	}

	state := provisioningState(body)

	//!TODO
	return state != statusInProgress
}

func jsonBody(respBody string) (map[string]any, error) {
	var jsonBody map[string]any
	if err := json.Unmarshal([]byte(respBody), &jsonBody); err != nil {
		return nil, err
	}
	return jsonBody, nil
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

func GetStatus(respBody string) (string, error) {
	jsonBody, err := jsonBody(respBody)
	if err != nil {
		return "", err
	}
	return status(jsonBody), nil
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
