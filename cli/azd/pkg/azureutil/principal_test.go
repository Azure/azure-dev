// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetOidClaimFromAccessToken(t *testing.T) {
	var oid string
	var err error

	// generated from jwt.io, a single claim named oid with the value "this-is-a-test"
	oid, err = getOidClaimFromAccessToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOiJ0aGlzLWlzLWEtdGVzdCJ9.vrKZx2J7-hsydI4rzdFVHqU1S6lHqLT95VSPx2RfQ04") // cspell: disable-line
	require.NoError(t, err)
	require.Equal(t, "this-is-a-test", oid)

	// generated from jwt.io, a single claim named oid with a complex value (a dictionary)
	_, err = getOidClaimFromAccessToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOnsidGVzdCI6ImZhaWwifX0.2BUeQShdNcyx3YDbByleQm-CZseB24gNCteLxTyKkQs") // cspell: disable-line
	require.Error(t, err)

	// generated from jwt.io, a single claim not named oid.
	_, err = getOidClaimFromAccessToken("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0ZXN0IjoiZmFpbCJ9.0Stzv5ZHG96ss-0_AnANqZfVLoULCtivJCE8AVWFZi8") // cspell: disable-line
	require.Error(t, err)

	_, err = getOidClaimFromAccessToken("not-a-token")
	require.Error(t, err)
}
