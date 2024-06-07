package auth

import "fmt"

const (
	//#nosec G101 -- This is a false positive
	remoteAuthCredName string = "RemoteCredential"

	contentTypeHeader   string = "Content-Type"
	authorizationHeader string = "Authorization"
)

func remoteCredentialError(err error) error {
	return fmt.Errorf("%s: %w", remoteAuthCredName, err)
}
