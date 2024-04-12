// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/vsrpc"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type vsServerFlags struct {
	global *internal.GlobalCommandOptions
	port   int
	useTls bool
}

func (s *vsServerFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	s.global = global
	local.IntVar(&s.port, "port", 0, "Port to listen on (0 for random port).")
	local.BoolVar(&s.useTls, "use-tls", false, "Use TLS to secure the connection.")
}

func newVsServerFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *vsServerFlags {
	flags := &vsServerFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newVsServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Hidden: true,
		Use:    "vs-server",
		Short:  "Run Server",
	}

	return cmd
}

type vsServerAction struct {
	rootContainer *ioc.NestedContainer
	flags         *vsServerFlags
}

func newVsServerAction(rootContainer *ioc.NestedContainer, flags *vsServerFlags) actions.Action {
	return &vsServerAction{
		rootContainer: rootContainer,
		flags:         flags,
	}
}

func (s *vsServerAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.flags.port))
	if err != nil {
		return nil, err
	}

	var versionRes contracts.VersionResult
	versionSpec := internal.VersionInfo()

	versionRes.Azd.Commit = versionSpec.Commit
	versionRes.Azd.Version = versionSpec.Version.String()

	res := contracts.VsServerResult{
		Port:          listener.Addr().(*net.TCPAddr).Port,
		Pid:           os.Getpid(),
		VersionResult: versionRes,
	}

	if s.flags.useTls {
		cert, derBytes, err := generateCertificate()
		if err != nil {
			return nil, err
		}

		config := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
			Certificates: []tls.Certificate{cert},
		}

		listener = tls.NewListener(listener, config)
		res.CertificateBytes = to.Ptr(base64.StdEncoding.EncodeToString(derBytes))
	}

	resString, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}

	fmt.Printf("%s\n", string(resString))

	return nil, vsrpc.NewServer(s.rootContainer).Serve(listener)
}

// generateCertificate generates a self-signed certificate for use in the server. It returns the tls.Certificate (for use
// in constructing a *tls.Config, so you use it with tls.NewListener()) and the raw bytes of the DER-encoded certificate.
func generateCertificate() (tls.Certificate, []byte, error) {
	// Derived from https://go.dev/src/crypto/tls/generate_cert.go
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Azure Developer CLI"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(7 * 24 * time.Hour),

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	var certBuf bytes.Buffer

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	if err := pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return tls.Certificate{}, nil, err
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	var keyBuf bytes.Buffer

	if err := pem.Encode(&keyBuf, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return tls.Certificate{}, nil, err
	}

	cert, err := tls.X509KeyPair(certBuf.Bytes(), keyBuf.Bytes())
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	return cert, derBytes, nil
}
