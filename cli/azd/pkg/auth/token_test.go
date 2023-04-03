package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetOidFromAccessToken(t *testing.T) {
	var oid string
	var err error

	// generated from jwt.io, a single claim named oid with the value "this-is-a-test"
	oid, err = GetOidFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOiJ0aGlzLWlzLWEtdGVzdCJ9.vrKZx2J7-hsydI4rzdFVHqU1S6lHqLT95VSPx2RfQ04",
	)
	require.NoError(t, err)
	require.Equal(t, "this-is-a-test", oid)

	// generated from jwt.io, a single claim named oid with a complex value (a dictionary)
	_, err = GetOidFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJvaWQiOnsidGVzdCI6ImZhaWwifX0.2BUeQShdNcyx3YDbByleQm-CZseB24gNCteLxTyKkQs",
	)
	require.Error(t, err)

	// generated from jwt.io, a single claim not named oid.
	_, err = GetOidFromAccessToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0ZXN0IjoiZmFpbCJ9.0Stzv5ZHG96ss-0_AnANqZfVLoULCtivJCE8AVWFZi8",
	)
	require.Error(t, err)

	_, err = GetOidFromAccessToken("not-a-token")
	require.Error(t, err)
}

func TestGetTenantIdFromToken(t *testing.T) {
	var tid string
	var err error

	// generated from jwt.io, a single claim named tid with the value "test-tenant"
	tid, err = GetTenantIdFromToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0aWQiOiJ0ZXN0LXRlbmFudCJ9.4GOivz4m6aBK_k5S2sRhS83KX2Nq5dNWfhYXTJ7Kw8I",
	)
	require.NoError(t, err)
	require.Equal(t, "test-tenant", tid)

	// generated from jwt.io, a single claim named tid with a complex value (a dictionary)
	_, err = GetTenantIdFromToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0aWQiOnsidGVzdCI6ImZhaWwifX0.UD2DqSW_P4JAaWcXxC1t8K20IO9laA4jg1kmz356jg8",
	)
	require.Error(t, err)

	// generated from jwt.io, a single claim not named tid.
	_, err = GetTenantIdFromToken(
		// cspell: disable-next-line
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ0ZXN0IjoiZmFpbCJ9.0Stzv5ZHG96ss-0_AnANqZfVLoULCtivJCE8AVWFZi8",
	)
	require.Error(t, err)

	_, err = GetTenantIdFromToken("not-a-token")
	require.Error(t, err)

}
