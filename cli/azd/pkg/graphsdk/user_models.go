package graphsdk

// A Microsoft Graph UserProfile entity.
type UserProfile struct {
	Id                string   `json:"id"`
	DisplayName       string   `json:"displayName"`
	GivenName         string   `json:"givenName"`
	Surname           string   `json:"surname"`
	JobTitle          string   `json:"jobTitle"`
	Mail              string   `json:"mail"`
	OfficeLocation    string   `json:"officeLocation"`
	UserPrincipalName string   `json:"userPrincipalName"`
	BusinessPhones    []string `json:"businessPhones"`
}
