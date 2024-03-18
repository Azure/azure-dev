package binding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetSourceResourceInfo(t *testing.T) {
	tests := []struct {
		name         string
		resourceType SourceResourceType
		resourceName string
		env          map[string]string
		expected     []string
		expectErr    bool
	}{
		{
			name:         "Test with default key name",
			resourceType: SourceTypeContainerApp,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1"): "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with fallback key name",
			resourceType: SourceTypeContainerApp,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingSourceFallbackKey, "RESOURCE1"): "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with sub-resource key names",
			resourceType: SourceTypeSpringApp,
			resourceName: "Resource2",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE2"):     "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE2_APP"): "Value2",
			},
			expected:  []string{"Value1", "Value2"},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getSourceResourceInfo(tt.resourceType, tt.resourceName, tt.env)
			if (err != nil) != tt.expectErr {
				t.Errorf("getSourceResourceInfo() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getSourceResourceInfo() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetTargetResourceInfo(t *testing.T) {
	tests := []struct {
		name         string
		resourceType TargetResourceType
		resourceName string
		env          map[string]string
		expected     []string
		expectErr    bool
	}{
		{
			name:         "Test with default key name",
			resourceType: TargetTypeContainerApp,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1"): "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with fallback key name",
			resourceType: TargetTypeContainerApp,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingSourceFallbackKey, "RESOURCE1"): "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with sub-resource key names",
			resourceType: TargetTypeSpringApp,
			resourceName: "Resource2",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE2"):     "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE2_APP"): "Value2",
			},
			expected:  []string{"Value1", "Value2"},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTargetResourceInfo(tt.resourceType, tt.resourceName, tt.env)
			if (err != nil) != tt.expectErr {
				t.Errorf("getTargetResourceInfo() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getTargetResourceInfo() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetStoreResourceInfo(t *testing.T) {
	tests := []struct {
		name         string
		storeType    StoreResourceType
		resourceName string
		env          map[string]string
		expected     []string
		expectErr    bool
	}{
		{
			name:         "Test with default key name",
			storeType:    StoreTypeAppConfig,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1"): "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with fallback key name",
			storeType:    StoreTypeAppConfig,
			resourceName: "Resource1",
			env: map[string]string{
				BindingStoreFallbackKey: "Value1",
			},
			expected:  []string{"Value1"},
			expectErr: false,
		},
		{
			name:         "Test with non-existing resource",
			storeType:    StoreTypeAppConfig,
			resourceName: "Resource2",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1"): "Value1",
			},
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getStoreResourceInfo(tt.storeType, tt.resourceName, tt.env)
			if (err != nil) != tt.expectErr {
				t.Errorf("getStoreResourceInfo() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getStoreResourceInfo() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetTargetSecretInfo(t *testing.T) {
	tests := []struct {
		name         string
		resourceType TargetResourceType
		resourceName string
		env          map[string]string
		expected     []string
		expectErr    bool
	}{
		{
			name:         "Test with default keys",
			resourceType: TargetTypePostgreSqlFlexible,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_USERNAME":        "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_KEYVAULT": "Value2",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_NAME":     "Value3",
			},
			expected:  []string{"Value1", "Value2", "Value3"},
			expectErr: false,
		},
		{
			name:         "Test with fallback keys",
			resourceType: TargetTypePostgreSqlFlexible,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_USERNAME":    "Value1",
				BindingKeyvaultFallbackKey:                                    "Value2",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_NAME": "Value3",
			},
			expected:  []string{"Value1", "Value2", "Value3"},
			expectErr: false,
		},
		{
			name:         "Test with missing key: keyvault name",
			resourceType: TargetTypePostgreSqlFlexible,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_NAME": "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_USERNAME":    "Value2",
			},
			expected:  nil,
			expectErr: true,
		},
		{
			name:         "Test with missing key: keyvault secret name",
			resourceType: TargetTypePostgreSqlFlexible,
			resourceName: "Resource1",
			env: map[string]string{
				BindingKeyvaultFallbackKey:                                 "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_USERNAME": "Value2",
			},
			expected:  nil,
			expectErr: true,
		},
		{
			name:         "Test with missing key: user name",
			resourceType: TargetTypePostgreSqlFlexible,
			resourceName: "Resource1",
			env: map[string]string{
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_KEYVAULT": "Value1",
				fmt.Sprintf(BindingResourceKey, "RESOURCE1") + "_SECRET_NAME":     "Value2",
			},
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getTargetSecretInfo(tt.resourceType, tt.resourceName, tt.env)
			if (err != nil) != tt.expectErr {
				t.Errorf("getTargetSecretInfo() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getTargetSecretInfo() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFormatResourceId(t *testing.T) {
	type testCase struct {
		name              string
		subscriptionId    string
		resourceGroupName string
		resourceNames     []string
		resourceIdFormat  string
		expected          string
		expectError       bool
	}

	testCases := []testCase{
		{
			name:              "ValidResourceId",
			subscriptionId:    "subId",
			resourceGroupName: "rgName",
			resourceNames:     []string{"flexibleServerName", "databaseName"},
			resourceIdFormat:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s/databases/%s",
			expected:          "/subscriptions/subId/resourceGroups/rgName/providers/Microsoft.DBforPostgreSQL/flexibleServers/flexibleServerName/databases/databaseName",
			expectError:       false,
		},
		{
			name:              "MismatchedFormat",
			subscriptionId:    "subId",
			resourceGroupName: "rgName",
			resourceNames:     []string{"flexibleServerName"},
			resourceIdFormat:  "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DBforPostgreSQL/flexibleServers/%s/databases/%s",
			expected:          "",
			expectError:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := formatResourceId(tc.subscriptionId, tc.resourceGroupName, tc.resourceNames, tc.resourceIdFormat)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, actual)
			}
		})
	}
}

func TestGetResourceId(t *testing.T) {
	type args struct {
		subscriptionId    string
		resourceGroupName string
		resourceType      interface{}
		resourceName      string
		env               map[string]string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "Test with SourceTypeWebApp",
			args: args{
				subscriptionId:    "subId",
				resourceGroupName: "rgName",
				resourceType:      SourceTypeWebApp,
				resourceName:      "sourceResource",
				env: map[string]string{
					fmt.Sprintf(BindingSourceFallbackKey, "SOURCERESOURCE"): "sourceResourceValue",
				},
			},
			want:    "/subscriptions/subId/resourceGroups/rgName/providers/Microsoft.Web/sites/sourceResourceValue",
			wantErr: false,
		},
		{
			name: "Test with TargetTypeStorageAccount",
			args: args{
				subscriptionId:    "subId",
				resourceGroupName: "rgName",
				resourceType:      TargetTypeStorageAccount,
				resourceName:      "targetResource",
				env: map[string]string{
					fmt.Sprintf(BindingResourceKey, "TARGETRESOURCE"): "targetResourceValue",
				},
			},
			want:    "/subscriptions/subId/resourceGroups/rgName/providers/Microsoft.Storage/storageAccounts/targetResourceValue",
			wantErr: false,
		},
		{
			name: "Test with StoreTypeAppConfig",
			args: args{
				subscriptionId:    "subId",
				resourceGroupName: "rgName",
				resourceType:      StoreTypeAppConfig,
				resourceName:      "storeResource",
				env: map[string]string{
					fmt.Sprintf(BindingResourceKey, "STORERESOURCE"): "storeResourceValue",
				},
			},
			want:    "/subscriptions/subId/resourceGroups/rgName/providers/Microsoft.AppConfiguration/configurationStores/storeResourceValue",
			wantErr: false,
		},
		{
			name: "Test with missing env value",
			args: args{
				subscriptionId:    "subId",
				resourceGroupName: "rgName",
				resourceType:      SourceTypeWebApp,
				resourceName:      "missingResource",
				env:               map[string]string{},
			},
			want:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getResourceId(tt.args.subscriptionId, tt.args.resourceGroupName, tt.args.resourceType, tt.args.resourceName, tt.args.env)
			if (err != nil) != tt.wantErr {
				t.Errorf("getResourceId() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getResourceId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetLinkerName(t *testing.T) {
	type args struct {
		bindingConfig *BindingConfig
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "Test with user provided name",
			args: args{
				bindingConfig: &BindingConfig{
					Name:           "userProvidedName",
					TargetType:     TargetTypeStorageAccount,
					TargetResource: "targetResource",
				},
			},
			want: "userProvidedName",
		},
		{
			name: "Test without user provided name",
			args: args{
				bindingConfig: &BindingConfig{
					Name:           "",
					TargetType:     TargetTypeStorageAccount,
					TargetResource: "targetResource",
				},
			},
			want: fmt.Sprintf("%s_%s", TargetTypeStorageAccount, "targetResource"),
		},
		{
			name: "Test with long generated name",
			args: args{
				bindingConfig: &BindingConfig{
					Name:           "",
					TargetType:     "longTargetTypeWithMoreThan50Characters",
					TargetResource: "longTargetResourceWithMoreThan50Characters",
				},
			},
			want: "longTargetTypeWithMoreThan50Characters_longTargetResourceWithMoreThan50Characters"[:50],
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getLinkerName(tt.args.bindingConfig); got != tt.want {
				t.Errorf("getLinkerName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetrievePassword(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Test with connection string",
			input:    "host=localhost;user=test;password=secret;dbname=testdb",
			expected: "secret",
		},
		{
			name:     "Test with connection string and seperator",
			input:    "Host=localhost User=test Password=secret Dbname=testdb",
			expected: "secret",
		},
		{
			name:     "Test with password only",
			input:    "password=secret",
			expected: "secret",
		},
		{
			name:     "Test with no password key",
			input:    "host=localhost;user=test;dbname=testdb",
			expected: "host=localhost;user=test;dbname=testdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retrievePassword(tt.input); got != tt.expected {
				t.Errorf("retrievePassword() = %v, want %v", got, tt.expected)
			}
		})
	}
}
