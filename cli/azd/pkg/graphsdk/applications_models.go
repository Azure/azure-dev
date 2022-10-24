package graphsdk

import "time"

// A Microsoft Graph Application entity.
type Application struct {
	Id                  *string                          `json:"id"`
	AppId               *string                          `json:"appId"`
	DisplayName         string                           `json:"displayName"`
	Description         *string                          `json:"description"`
	PasswordCredentials []*ApplicationPasswordCredential `json:"passwordCredentials"`
}

type ApplicationCreateRequest struct {
	Application
}

// A list of applications returned from the Microsoft Graph.
type ApplicationListResponse struct {
	Value []Application `json:"value"`
}

type ApplicationAddPasswordRequest struct {
	PasswordCredential ApplicationPasswordCredential `json:"passwordCredential"`
}

type ApplicationPasswordCredential struct {
	KeyId               *string    `json:"keyId"`
	CustomKeyIdentifier *string    `json:"customKeyIdentifier"`
	DisplayName         *string    `json:"displayName"`
	StartDateTime       *time.Time `json:"startDateTime"`
	EndDateTime         *time.Time `json:"endDateTime"`
	SecretText          *string    `json:"secretText"`
	Hint                *string    `json:"hint"`
}

type ApplicationRemovePasswordRequest struct {
	KeyId string `json:"keyId"`
}

type ApplicationAddPasswordResponse struct {
	ApplicationPasswordCredential
}
