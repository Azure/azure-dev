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
	oid, err = getOidClaimFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOiJ0aGlzLWlzLWEtdGVzdCJ9.vrKZx2J7-hsydI4rzdFVHqU1S6lHqLT95VSPx2RfQ04",
	)
	require.NoError(t, err)
	require.Equal(t, "this-is-a-test", oid)

	// generated from jwt.io, a single claim named oid with a complex value (a dictionary)
	_, err = getOidClaimFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOnsidGVzdCI6ImZhaWwifX0.2BUeQShdNcyx3YDbByleQm-CZseB24gNCteLxTyKkQs",
	)
	require.Error(t, err)

	// generated from jwt.io, a single claim not named oid.
	_, err = getOidClaimFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0ZXN0IjoiZmFpbCJ9.0Stzv5ZHG96ss-0_AnANqZfVLoULCtivJCE8AVWFZi8",
	)
	require.Error(t, err)

	_, err = getOidClaimFromAccessToken("not-a-token")
	require.Error(t, err)
}
