// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
)

func TestShow(t *testing.T) {
	pp := &Show{
		AppName: "Foo",
		Services: []*ShowService{
			{
				Name:      "xx",
				IngresUrl: "bar",
			},
		},
		Environments: []*ShowEnvironment{
			{
				Name:      "foo",
				IsCurrent: true,
			},
			{
				Name:      "Bar",
				IsCurrent: false,
			},
		},
		AzurePortalLink: "foo.com",
	}

	output := pp.ToString("")
	snapshot.SnapshotT(t, output)
}

func TestShowNoEnv(t *testing.T) {
	pp := &Show{
		AppName: "Foo",
		Services: []*ShowService{
			{
				Name:      "xx",
				IngresUrl: "bar",
			},
		},
		Environments:    []*ShowEnvironment{},
		AzurePortalLink: "foo.com",
	}

	output := pp.ToString("")
	snapshot.SnapshotT(t, output)
}

func TestShowNoPortalLink(t *testing.T) {
	pp := &Show{
		AppName: "Foo",
		Services: []*ShowService{
			{
				Name:      "xx",
				IngresUrl: "bar",
			},
		},
		Environments:    []*ShowEnvironment{},
		AzurePortalLink: "",
	}

	output := pp.ToString("")
	snapshot.SnapshotT(t, output)
}

func TestShowEmpty(t *testing.T) {
	pp := &Show{
		AppName:         "Foo",
		Services:        []*ShowService{},
		Environments:    []*ShowEnvironment{},
		AzurePortalLink: "",
	}

	output := pp.ToString("")
	snapshot.SnapshotT(t, output)
}

func TestShowRemoteAndCurrent(t *testing.T) {
	pp := &Show{
		AppName:  "Foo",
		Services: []*ShowService{},
		Environments: []*ShowEnvironment{
			{
				Name:      "foo",
				IsCurrent: true,
			},
			{
				Name: "bar",
			},
			{
				Name:     "vh",
				IsRemote: true,
			},
		},
		AzurePortalLink: "",
	}

	output := pp.ToString("")
	snapshot.SnapshotT(t, output)
}
