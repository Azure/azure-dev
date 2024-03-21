// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package oneauth

type Account struct {
	AssociatedApps []string
	DisplayName    string
	ID             string
	Username       string
}

// FilterValue implements bubble/list.Item, enabling using this type directly in the account picker.
func (Account) FilterValue() string { return "" }

func (a Account) IsZero() bool {
	return a.ID == "" && a.Username == "" && a.DisplayName == "" && len(a.AssociatedApps) == 0
}
