// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package events provides definitions and functions related to the definition of telemetry events.
package events

// Command event names follow the convention cmd.<command invocation path with spaces replaced by .>.
//
// Examples:
//   - cmd.auth.login
//   - cmd.init
//   - cmd.up
const CommandEventPrefix = "cmd."

// Prefix for vsrpc events.
const VsRpcEventPrefix = "vsrpc."

// BicepInstallEvent is the name of the event which tracks the overall bicep install operation.
const BicepInstallEvent = "tools.bicep.install"

// GitHubCliInstallEvent is the name of the event which tracks the overall GitHub cli install operation.
const GitHubCliInstallEvent = "tools.gh.install"

// PackCliInstallEvent is the name of the event which tracks the overall pack cli install operation.
const PackCliInstallEvent = "tools.pack.install"

// PackBuildEvent is the name of the event which tracks the overall pack build operation.
const PackBuildEvent = "tools.pack.build"

// AccountSubscriptionsListEvent is the name of the event which tracks listing of account subscriptions .
// See fields.AccountSubscriptionsListTenantsFound for additional event fields.
const AccountSubscriptionsListEvent = "account.subscriptions.list"
