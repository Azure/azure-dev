// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

func Test_httpPollDiscarder_BeforeSave(t *testing.T) {
	tests := []struct {
		name string
		in   []cassette.Interaction
		out  []cassette.Interaction
	}{
		{
			name: "Simple",
			in: []cassette.Interaction{
				locStart("1"),
				locPoll(locPollOptions{id: "1"}),
				locPoll(locPollOptions{id: "1"}),
				locPoll(locPollOptions{id: "1", newLocId: "2"}),
				locPoll(locPollOptions{id: "2", newLocId: "3"}),
				locPoll(locPollOptions{id: "3", done: true}),
			},
			out: []cassette.Interaction{
				locStart("3"),
				locPoll(locPollOptions{id: "3", done: true}),
			},
		},
		{
			name: "Concurrent",
			in: []cassette.Interaction{
				locStart("1"),
				locPoll(locPollOptions{id: "1"}),
				locStart("2"),
				locPoll(locPollOptions{id: "2"}),
				locStart("3"),
				locPoll(locPollOptions{id: "3"}),
				locPoll(locPollOptions{id: "2"}),
				locPoll(locPollOptions{id: "1"}),
				locPoll(locPollOptions{id: "3", done: true}),
				locPoll(locPollOptions{id: "2", done: true}),
				locPoll(locPollOptions{id: "1", done: true}),
			},
			out: []cassette.Interaction{
				locStart("1"),
				locStart("2"),
				locStart("3"),
				locPoll(locPollOptions{id: "3", done: true}),
				locPoll(locPollOptions{id: "2", done: true}),
				locPoll(locPollOptions{id: "1", done: true}),
			},
		},
		{
			name: "keeps non-polling interactions",
			in: []cassette.Interaction{
				other(),
				other(),
				locStart("1"),
				locPoll(locPollOptions{id: "1"}),
				other(),
				locPoll(locPollOptions{id: "1", done: true}),
				other(),
			},
			out: []cassette.Interaction{
				other(),
				other(),
				locStart("1"),
				other(),
				locPoll(locPollOptions{id: "1", done: true}),
				other(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &httpPollDiscarder{}
			for i := range tt.in {
				err := d.BeforeSave(&tt.in[i])
				require.NoError(t, err)
			}

			recorded := make([]cassette.Interaction, 0, len(tt.in))
			for _, i := range tt.in {
				if !i.DiscardOnSave {
					recorded = append(recorded, i)
				}
			}

			require.Equal(t, tt.out, recorded)
		})
	}
}

// a non-polling interaction
func other() cassette.Interaction {
	return cassette.Interaction{
		Request: cassette.Request{
			Method: "GET",
			URL:    "http://localhost:8080/other",
		},
		Response: cassette.Response{
			Code: http.StatusOK,
		},
	}
}

// A location polling start interaction
func locStart(id string) cassette.Interaction {
	if id == "" {
		id = "default"
	}
	return cassette.Interaction{
		Request: cassette.Request{
			Method: "PUT",
			URL:    "http://localhost:8080/locOp",
		},
		Response: cassette.Response{
			Code: http.StatusAccepted,
			Headers: map[string][]string{
				"Location": {
					fmt.Sprintf("http://localhost:8080/locOp/%s", id),
				},
			},
		},
	}
}

type locPollOptions struct {
	id       string
	newLocId string
	done     bool
}

// A location polling interaction
func locPoll(opt locPollOptions) cassette.Interaction {
	if opt.id == "" {
		opt.id = "default"
	}
	if opt.newLocId == "" {
		opt.newLocId = opt.id
	}
	code := http.StatusAccepted
	if opt.done {
		code = http.StatusOK
	}
	return cassette.Interaction{
		Request: cassette.Request{
			Method: "GET",
			URL:    fmt.Sprintf("http://localhost:8080/locOp/%s", opt.id),
		},
		Response: cassette.Response{
			Code: code,
			Headers: map[string][]string{
				"Location": {
					fmt.Sprintf("http://localhost:8080/locOp/%s", opt.newLocId),
				},
			},
		},
	}
}
