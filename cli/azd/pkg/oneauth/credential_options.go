// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package oneauth

type CredentialOptions struct {
	// Debug enables OneAuth logging, including PII.
	Debug bool
	// HomeAccountID of a previously authenticated user the credential
	// should attempt to authenticate from OneAuth's cache.
	HomeAccountID string
	// NoPrompt restricts the credential to silent authentication.
	// When true, authentication fail when it requires user interaction.
	NoPrompt bool
}
