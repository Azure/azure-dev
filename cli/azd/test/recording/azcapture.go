package recording

import (
	"encoding/json"
	"net/http"
	"net/url"

	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

const (
	Op       = "op"
	Async    = "async"
	Location = "location"
	Body     = "body"
)

type httpPollDiscarder struct {
	pollingOp *pollOperation
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
	if d.pollingOp != nil && pollingDone(d.pollingOp, i) {
		d.pollingOp = nil
	}

	if d.pollingOp != nil {
		i.DiscardOnSave = true
	}

	if op := pollingOperation(i); op != nil {
		d.pollingOp = op
	}

	return nil
}

func pollingOperation(i *cassette.Interaction) *pollOperation {
	if isOpPoll(i) {
		return Op
	}
	if isAsyncPoll(i) {
		return Async
	}
	if isLocationPoll(i) {
		return Location
	}
	if isBodyPoll(i) {
		return Body
	}

	return ""
}

type pollOperation struct {
	location url.URL

	done func(*cassette.Interaction) bool
}

type asyncPoller struct {
	location url.URL
}

func newAsyncPoll(i *cassette.Interaction) (*asyncPoller, error) {
	if i.Request.Method == "PUT" && i.Response.Headers.Get("Azure-AsyncOperation") != "" {
		url, err := url.Parse(i.Response.Headers.Get("Azure-AsyncOperation"))
		if err != nil {
			return nil, err
		}
		return &asyncPoller{
			location: *url,
		}, nil
	}

	return nil, nil
}

func (a *asyncPoller) Done(i *cassette.Interaction) bool {
	reqUrl, err := url.Parse(i.Request.URL)
	if err != nil {
		panic(err)
	}

	if reqUrl.String() == a.location.String() {

	}

	return i.Response.StatusCode == http.StatusOK
}

type opPoller struct {
	location url.URL
}

func newOpPoll(i *cassette.Interaction) (*opPoller, error) {
	if i.Request.Method == "PUT" && i.Response.Headers.Get("Operation-Location") != "" {
		url, err := url.Parse(i.Response.Headers.Get("Operation-Location"))
		if err != nil {
			return nil, err
		}
		return &opPoller{
			location: *url,
		}, nil
	}

	return nil, nil
}

type locPoller struct {
	location url.URL
}

func newLocationPoll(i *cassette.Interaction) (*locPoller, error) {
	if i.Request.Method == "PUT" && i.Response.Headers.Get("Location") != "" {
		url, err := url.Parse(i.Response.Headers.Get("Location"))
		if err != nil {
			return nil, err
		}
		return &locPoller{
			location: *url,
		}, nil
	}

	return nil, nil
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

func newBodyPoll(i *cassette.Interaction) (*bodyPoller, error) {
	if i.Request.Method == "PATCH" || i.Request.Method == "PUT" && i.Response.Code == http.StatusCreated {
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

	return nil, nil
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
