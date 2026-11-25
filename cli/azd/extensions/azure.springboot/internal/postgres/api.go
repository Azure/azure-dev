// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package postgres

import (
	"azurespringboot/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// AuthoredBootstrapRule is the legacy template-authored broad firewall rule name cleaned up when verified.
	AuthoredBootstrapRule = authoredBootstrapRule
	// CleanupTimeout bounds cancellation-independent temporary firewall and administrator cleanup.
	CleanupTimeout = postgresCleanupTimeout
)

func validationError(code, message, suggestion string) error {
	return exterrors.Validation(code, message, suggestion)
}

func configurationError(message string) error {
	return exterrors.Configuration(message)
}

func nonDisclosureError(err error) error {
	return exterrors.NonDisclosureError(err)
}

// FirewallReconciler verifies runtime outbound firewall rules and cleans up verified legacy bootstrap rules.
type FirewallReconciler = firewallReconciler

// NewFirewallReconciler creates a FirewallReconciler backed by client.
func NewFirewallReconciler(client *azdext.AzdClient) *FirewallReconciler {
	return newFirewallReconciler(client)
}

// AccessReconciler verifies Azure PostgreSQL resources and reconciles Entra roles and grants.
type AccessReconciler = postgresAccessReconciler

// NewAccessReconciler creates an AccessReconciler backed by client.
func NewAccessReconciler(client *azdext.AzdClient) *AccessReconciler {
	return newPostgresAccessReconciler(client)
}

// LocalAccessReconciler manages temporary local postprovision firewall/caller-admin access and recovery.
type LocalAccessReconciler = postgresLocalAccessReconciler

// NewLocalAccessReconciler creates a LocalAccessReconciler wrapping local.
func NewLocalAccessReconciler(local *AccessReconciler) *LocalAccessReconciler {
	return newPostgresLocalAccessReconciler(local)
}
