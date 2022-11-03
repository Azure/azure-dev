package graphsdk

type FederatedIdentityCredentialListResponse struct {
	Value []FederatedIdentityCredential `json:"value"`
}

type FederatedIdentityCredential struct {
	Id          *string  `json:"id"`
	Name        string   `json:"name"`
	Issuer      string   `json:"issuer"`
	Subject     string   `json:"subject"`
	Description *string  `json:"description"`
	Audiences   []string `json:"audiences"`
}
