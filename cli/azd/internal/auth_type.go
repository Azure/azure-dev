package internal

// AuthType defines different authentication types.
type AuthType string

const (
	AuthTypeUnspecified AuthType = "UNSPECIFIED"
	// Username and password, or key based authentication
	AuthtypePassword AuthType = "PASSWORD"
	// Connection string authentication
	AuthtypeConnectionString AuthType = "CONNECTION_STRING"
	// Microsoft EntraID token credential
	AuthtypeManagedIdentity AuthType = "MANAGED_IDENTITY"
)
