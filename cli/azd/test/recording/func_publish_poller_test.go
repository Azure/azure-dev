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

func Test_funcPublishPoller_BeforeSave(t *testing.T) {
	tests := []struct {
		name string
		in   []cassette.Interaction
		out  []cassette.Interaction
	}{
		{
			name: "Simple publish polling",
			in: []cassette.Interaction{
				funcPublishStart("deploy-123"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-123", status: 0}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-123", status: 1}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-123", status: 2}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-123", status: 4}), // success
			},
			out: []cassette.Interaction{
				funcPublishStart("deploy-123"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-123", status: 4}),
			},
		},
		{
			name: "Publish with failure",
			in: []cassette.Interaction{
				funcPublishStart("deploy-456"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-456", status: 0}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-456", status: 3}), // failed
			},
			out: []cassette.Interaction{
				funcPublishStart("deploy-456"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-456", status: 3}),
			},
		},
		{
			name: "Concurrent publish operations",
			in: []cassette.Interaction{
				funcPublishStart("deploy-1"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-1", status: 0}),
				funcPublishStart("deploy-2"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-2", status: 1}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-1", status: 2}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-2", status: 4}), // success
				funcPublishPoll(funcPublishPollOptions{id: "deploy-1", status: 4}), // success
			},
			out: []cassette.Interaction{
				funcPublishStart("deploy-1"),
				funcPublishStart("deploy-2"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-2", status: 4}),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-1", status: 4}),
			},
		},
		{
			name: "Mixed with other interactions",
			in: []cassette.Interaction{
				other(),
				funcPublishStart("deploy-789"),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-789", status: 0}),
				other(),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-789", status: 4}),
				other(),
			},
			out: []cassette.Interaction{
				other(),
				funcPublishStart("deploy-789"),
				other(),
				funcPublishPoll(funcPublishPollOptions{id: "deploy-789", status: 4}),
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

func funcPublishStart(deploymentId string) cassette.Interaction {
	return cassette.Interaction{
		Request: cassette.Request{
			Method: http.MethodPost,
			URL:    "https://myapp.scm.azurewebsites.net:443/api/publish?RemoteBuild=true",
		},
		Response: cassette.Response{
			Code: http.StatusAccepted,
			Body: fmt.Sprintf(`"%s"`, deploymentId),
			Headers: map[string][]string{
				"Location": {
					fmt.Sprintf("https://myapp.scm.azurewebsites.net/api/deployments/%s", deploymentId),
				},
			},
		},
	}
}

type funcPublishPollOptions struct {
	id     string
	status int
}

func funcPublishPoll(opt funcPublishPollOptions) cassette.Interaction {
	return cassette.Interaction{
		Request: cassette.Request{
			Method: http.MethodGet,
			URL:    fmt.Sprintf("https://myapp.scm.azurewebsites.net:443/api/deployments/%s", opt.id),
		},
		Response: cassette.Response{
			Code: http.StatusOK,
			Body: fmt.Sprintf(`{"id":"%s","status":%d}`, opt.id, opt.status),
		},
	}
}
