package account

// AZD Account configuration
type Account struct {
	DefaultSubscription *Subscription `json:"defaultSubscription"`
	DefaultLocation     *Location     `json:"defaultLocation"`
}

type Subscription struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	TenantId string `json:"tenantId"`
	// The tenant under which the user has access to the subscription.
	UserAccessTenantId string `json:"userAccessTenantId"`
	IsDefault          bool   `json:"isDefault,omitempty"`
}

type Location struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}
