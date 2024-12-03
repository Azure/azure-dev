package internal

// AuthType defines different authentication types.
type AuthType string

const (
	AuthTypeUnspecified AuthType = "Unspecified"
	// Username and password, or key based authentication
	AuthTypePassword AuthType = "Password"
	// Connection string authentication
	AuthTypeConnectionString AuthType = "ConnectionString"
	// Microsoft EntraID token credential
	AuthTypeUserAssignedManagedIdentity AuthType = "UserAssignedManagedIdentity"
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
