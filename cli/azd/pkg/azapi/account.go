package azapi

import (
	"regexp"
	"time"
)

var (
	isNotLoggedInMessageRegex = regexp.MustCompile(
		`(ERROR: No subscription found)|(Please run ('|")az login('|") to (setup account|access your accounts)\.)+`,
	)
	isRefreshTokenExpiredMessageRegex = regexp.MustCompile(`AADSTS(70043|700082)`)
)

// AzCliAccessToken represents the value returned by `az account get-access-token`
type AzCliAccessToken struct {
	AccessToken string
	ExpiresOn   *time.Time
}

func isNotLoggedInMessage(s string) bool {
	return isNotLoggedInMessageRegex.MatchString(s)
}

func isRefreshTokenExpiredMessage(s string) bool {
	return isRefreshTokenExpiredMessageRegex.MatchString(s)
}
