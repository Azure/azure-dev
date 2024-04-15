package binding

import (
	"testing"
)

func TestSourceResourceType_IsValid(t *testing.T) {
	tests := []struct {
		name string
		s    SourceResourceType
		want bool
	}{
		{"Valid SourceTypeAppService", SourceTypeAppService, true},
		{"Valid SourceTypeFunctionApp", SourceTypeFunctionApp, true},
		{"Invalid SourceResourceType", SourceResourceType("invalid"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("SourceResourceType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStoreResourceType_IsValid(t *testing.T) {
	tests := []struct {
		name string
		s    StoreResourceType
		want bool
	}{
		{"Valid StoreTypeAppConfig", StoreTypeAppConfig, true},
		{"Invalid StoreResourceType", StoreResourceType("invalid"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("StoreResourceType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTargetResourceType_IsValid(t *testing.T) {
	tests := []struct {
		name string
		s    TargetResourceType
		want bool
	}{
		{"Valid TargetTypeAppInsights", TargetTypeAppInsights, true},
		{"Valid TargetTypeCosmosDB", TargetTypeCosmosDB, true},
		{"Valid TargetTypeEventHub", TargetTypeEventHub, true},
		{"Valid TargetTypeMysqlFlexible", TargetTypeMysql, true},
		{"Valid TargetTypePostgreSqlFlexible", TargetTypePostgreSql, true},
		{"Valid TargetTypeRedis", TargetTypeRedis, true},
		{"Valid TargetTypeRedisEnterprise", TargetTypeRedisEnterprise, true},
		{"Valid TargetTypeServiceBus", TargetTypeServiceBus, true},
		{"Valid TargetTypeSignalR", TargetTypeSignalR, true},
		{"Valid TargetTypeSql", TargetTypeSql, true},
		{"Valid TargetTypeStorageAccount", TargetTypeStorageAccount, true},
		{"Valid TargetTypeWebPubSub", TargetTypeWebPubSub, true},
		{"Valid TargetTypeContainerApp", TargetTypeContainerApp, true},

		{"Invalid TargetResourceType", TargetResourceType("invalid"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("TargetResourceType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}
