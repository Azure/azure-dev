// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCertificate(t *testing.T) {
	t.Parallel()

	cert, derBytes, err := generateCertificate()
	require.NoError(t, err)
	require.NotNil(t, cert.Certificate)
	require.NotEmpty(t, derBytes)

	// Parse and verify the certificate
	parsedCert, err := x509.ParseCertificate(derBytes)
	require.NoError(t, err)

	assert.Equal(t, "Azure Developer CLI", parsedCert.Subject.Organization[0])
	// Cert stores 127.0.0.1 as 4-byte IPv4; net.ParseIP returns 16-byte form, so compare with To4
	assert.True(t, parsedCert.IPAddresses[0].Equal(net.IPv4(127, 0, 0, 1)))
	assert.True(t, parsedCert.NotBefore.Before(time.Now().Add(time.Second)))
	assert.True(t, parsedCert.NotAfter.After(time.Now()))
	assert.Contains(t, parsedCert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
}
