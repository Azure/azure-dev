package contracts

type VsServerResult struct {
	Port int `json:"port"`
	Pid  int `json:"pid"`
	// The certificate that the server uses to secure the TLS connection. This is the base 64 encoding of the raw bytes of
	// the DER encoded certificate. The client should use this to verify the server's identity. In .NET, You can use
	// `X509Certificate2.ctor(byte[])` to construct a certificate from these bytes, after Base64 decoding. When TLS is not
	// used, this will be null.
	CertificateBytes *string `json:"certificateBytes,omitempty"`
	VersionResult
}
