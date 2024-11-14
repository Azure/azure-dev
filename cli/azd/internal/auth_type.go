package internal

// AuthType defines different authentication types.
type AuthType string

const (
	AuthTypeUnspecified AuthType = "UNSPECIFIED"
	// Username and password, or key based authentication
	AuthTypePassword AuthType = "PASSWORD"
	// Connection string authentication
	AuthTypeConnectionString AuthType = "CONNECTION_STRING"
	// Microsoft EntraID token credential
	AuthTypeUserAssignedManagedIdentity AuthType = "USER_ASSIGNED_MANAGED_IDENTITY"
)

func GetAuthTypeDescription(authType AuthType) string {
	switch authType {
	case AuthTypeUnspecified:
		return "Unspecified"
	case AuthTypePassword:
		return "Username and password"
	case AuthTypeConnectionString:
		return "Connection string"
	case AuthTypeUserAssignedManagedIdentity:
		return "User assigned managed identity"
	}
	panic("unknown auth type")
}
