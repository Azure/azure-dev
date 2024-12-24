package internal

// AuthType defines different authentication types.
type AuthType string

const (
	AuthTypeUnspecified AuthType = "unspecified"
	// Username and password, or key based authentication
	AuthTypePassword AuthType = "password"
	// Connection string authentication
	AuthTypeConnectionString AuthType = "connectionString"
	// Microsoft EntraID token credential
	AuthTypeUserAssignedManagedIdentity AuthType = "userAssignedManagedIdentity"
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
	default:
		return "Unspecified"
	}
}
