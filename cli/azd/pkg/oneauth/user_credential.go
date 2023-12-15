// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package oneauth

import "github.com/Azure/azure-sdk-for-go/sdk/azcore"

type UserCredential interface {
	azcore.TokenCredential
	HomeAccountID() string
}
