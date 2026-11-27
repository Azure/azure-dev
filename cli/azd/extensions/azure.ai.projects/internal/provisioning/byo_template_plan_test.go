// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"azure.ai.projects/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYOPlanNestedDeploymentName(t *testing.T) {
	scope := planScope{subscriptionID: "sub", subscriptionARMID: "/subscriptions/sub",
		group: "rg", groupARMID: "/subscriptions/sub/resourceGroups/rg", groupExists: true}
	for _, name := range []string{"inner", "[parameters('runtimeName')]"} {
		t.Run(name, func(t *testing.T) {
			resolver := &templatePlanResolver{scopes: map[string]planScope{"sub/rg": scope}}
			template := map[string]any{"resources": map[string]any{"nested": map[string]any{
				"type": "Microsoft.Resources/deployments", "name": name,
				"properties": map[string]any{
					"expressionEvaluationOptions": map[string]any{"scope": "inner"},
					"template": map[string]any{"resources": map[string]any{"identity": map[string]any{
						"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "name": "[deployment().name]",
					}}},
				},
			}}}
			env := resolver.environment(template, nil, scope, "root", "outer")
			resolver.walk(env, "", 0)
			child := env.resources["nested"].module()
			require.NotNil(t, child)
			id, missing := child.resources["identity"].id()
			if name == "inner" {
				assert.Equal(t, scope.groupARMID+"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/inner", id)
				assert.Empty(t, missing)
			} else {
				assert.Empty(t, id)
				assert.Contains(t, missing, "runtimeName")
			}
			assert.Equal(t, "outer", planField(env.value("[deployment()]"), "name"))
		})
	}
}

func TestBYOPlanAccountSelectionAndHostingResolution(t *testing.T) {
	const (
		clusterA = "/subscriptions/sub-a/resourceGroups/rg-a/providers/" +
			"Microsoft.ContainerService/managedClusters/aks-a"
		clusterB = "/subscriptions/sub-b/resourceGroups/rg-b/providers/" +
			"Microsoft.ContainerService/managedClusters/aks-b"
		storageA = "/subscriptions/sub-a/resourceGroups/rg-a/providers/Microsoft.Storage/storageAccounts/storea"
		storageB = "/subscriptions/sub-b/resourceGroups/rg-b/providers/Microsoft.Storage/storageAccounts/storeb"
		hostingA = "/subscriptions/sub-a/resourceGroups/rg-a/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/hosta"
		hostingB = "/subscriptions/sub-b/resourceGroups/rg-b/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/hostb"
		workloadA = "/subscriptions/sub-a/resourceGroups/rg-a/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/worka"
		workloadB = "/subscriptions/sub-b/resourceGroups/rg-b/providers/" +
			"Microsoft.ManagedIdentity/userAssignedIdentities/workb"
		subnetA = "/subscriptions/net-sub/resourceGroups/net-rg/providers/" +
			"Microsoft.Network/virtualNetworks/net/subnets/a"
		subnetB = "/subscriptions/net-sub/resourceGroups/net-rg/providers/" +
			"Microsoft.Network/virtualNetworks/net/subnets/b"
	)
	scopes := map[string]planScope{
		"sub-a/": {subscriptionID: "sub-a", subscriptionARMID: "/subscriptions/sub-a"},
		"sub-a/rg-a": {
			subscriptionID: "sub-a", subscriptionARMID: "/subscriptions/sub-a",
			group: "rg-a", groupARMID: "/subscriptions/sub-a/resourceGroups/rg-a", groupExists: true,
		},
		"sub-b/": {subscriptionID: "sub-b", subscriptionARMID: "/subscriptions/sub-b"},
		"sub-b/rg-b": {
			subscriptionID: "sub-b", subscriptionARMID: "/subscriptions/sub-b",
			group: "rg-b", groupARMID: "/subscriptions/sub-b/resourceGroups/rg-b", groupExists: true,
		},
	}
	cfgA := map[string]any{
		"hostingType": "ManagedCluster", "clusterResourceId": clusterA,
		"storageAccountResourceId": storageA, "hostingManagementIdentityResourceId": hostingA,
		"workloadIdentityResourceId": workloadA,
	}
	cfgB := map[string]any{
		"hostingType": "ManagedCluster", "clusterResourceId": clusterB,
		"storageAccountResourceId": storageB, "hostingManagementIdentityResourceId": hostingB,
		"workloadIdentityResourceId": workloadB,
	}
	subnet := func(id string) []any {
		return []any{map[string]any{"scenario": "agent", "subnetArmId": id, "useMicrosoftManagedNetwork": false}}
	}
	mod := func(name, sub, rg string, resources, outputs map[string]any) map[string]any {
		return map[string]any{
			"type": "Microsoft.Resources/deployments", "name": name, "subscriptionId": sub, "resourceGroup": rg,
			"properties": map[string]any{
				"expressionEvaluationOptions": map[string]any{"scope": "inner"},
				"template": map[string]any{
					"languageVersion": "2.0", "resources": resources, "outputs": outputs,
				},
			},
		}
	}

	tests := []struct {
		name               string
		resources, outputs map[string]any
		wantSub            string
		wantClusterID      string
		wantClusterMissing string
		wantSubnetID       string
		wantSubnetMissing  string
		wantSingle         bool
		wantProven         bool
		wantErr            string
	}{
		{
			name: "single BYO account with dynamic property key and plain or inactive bystanders",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"activeBYO": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{
						"[concat('agentHosting', 'Configurations')]": []any{cfgA},
						"[concat('network', 'Injections')]":          subnet(subnetA),
					},
				},
				"plain": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "[uniqueString('unused-plain-name')]",
					"properties": map[string]any{"allowProjectManagement": true},
				},
				"nonAKS": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "non-aks",
					"properties": map[string]any{
						"agentHostingConfigurations": []any{map[string]any{"hostingType": "None"}},
					},
				},
				"inactive": map[string]any{
					"condition": false, "type": "Microsoft.CognitiveServices/accounts", "name": "inactive-b",
					"properties": "[uniqueString('unused-inactive')]",
				},
			}, map[string]any{
				"UNUSED": map[string]any{"type": "string", "value": "[uniqueString('unused-child')]"},
			})},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "[parameters('dynamicAccount')]"},
				"UNUSED_ROOT":           map[string]any{"type": "string", "value": "[uniqueString('unused-root')]"},
			},
			wantSub: "sub-a", wantClusterID: clusterA, wantSubnetID: subnetA, wantProven: true,
		},
		{
			name: "output selects unresolved account B without evaluating bystander A properties",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
						"properties": "[createObject('agentHostingConfigurations', " +
							"createArray(createObject('hostingType', 'ManagedCluster', " +
							"'clusterResourceId', uniqueString('bystander-must-not-run'))))]",
					},
				}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
					"accountB": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
						"properties": map[string]any{
							"agentHostingConfigurations": "[parameters('runtimeHostingB')]",
							"networkInjections":          subnet(subnetB),
						},
					},
				}, map[string]any{
					"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "ACCOUNT-B"},
				}),
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{
					"type": "string",
					"value": "[if(empty(reference('modB').outputs), '', " +
						"reference('modB').outputs.AZURE_AI_ACCOUNT_NAME.value)]",
				},
				"UNUSED_ROOT": map[string]any{"type": "string", "value": "[uniqueString('unused-root')]"},
			},
			wantSub: "sub-b", wantClusterMissing: "runtimeHostingB", wantSubnetID: subnetB,
		},
		{
			name: "output selects account B hidden in unexpanded linked module",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{
						"agentHostingConfigurations": []any{cfgA}, "networkInjections": subnet(subnetA),
					},
				}}, nil),
				"linkedB": map[string]any{
					"type": "Microsoft.Resources/deployments", "name": "linked-b",
					"properties": map[string]any{"templateLink": map[string]any{"uri": "https://example.test/b.json"}},
				},
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "account-b"},
			},
			wantSub:            "sub-a",
			wantClusterMissing: "did not match a local Foundry account",
			wantSubnetMissing:  "linkedB",
		},
		{
			name: "output selects resolved account B and ignores bystander invalid hostingType or unknown properties",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
						"properties": map[string]any{"agentHostingConfigurations": []any{map[string]any{
							"hostingType": "Invalid", "clusterResourceId": clusterA,
						}}},
					},
					"accountC": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "account-c",
						"properties": "[parameters('unknownPropsC')]",
					},
				}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
					"properties": map[string]any{
						"agentHostingConfigurations": []any{cfgB}, "networkInjections": subnet(subnetB),
					},
				}}, nil),
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "account-b"},
			},
			wantSub:       "sub-b",
			wantClusterID: clusterB,
			wantSubnetID:  subnetB,
			wantProven:    true,
		},
		{
			name: "output with unknown-name plain bystander retains ambiguity across resource groups",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				}}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"plainUnknownName": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "[parameters('dynPlainName')]",
					"properties": map[string]any{"allowProjectManagement": true},
				}}, nil),
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "account-a"},
			},
			wantSub:            "sub-a",
			wantClusterMissing: "dynPlainName",
			wantSubnetMissing:  "dynPlainName",
		},
		{
			name: "plain bystander alongside sole BYO account with opaque workload keeps singleAccountHosting",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
						"properties": map[string]any{"agentHostingConfigurations": []any{map[string]any{
							"hostingType":                         "ManagedCluster",
							"clusterResourceId":                   clusterA,
							"storageAccountResourceId":            storageA,
							"hostingManagementIdentityResourceId": hostingA,
							"workloadIdentityResourceId":          "[parameters('opaqueWorkload')]",
						}}},
					},
					"speechCog": map[string]any{
						"type": "Microsoft.CognitiveServices/accounts", "name": "speech-cog",
						"properties": map[string]any{"allowProjectManagement": false},
					},
				}, nil),
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "account-a"},
			},
			wantSub:       "sub-a",
			wantClusterID: clusterA,
			wantSingle:    true,
			wantProven:    true,
		},
		{
			name: "selected single account with invalid hostingType reports error",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
				"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
				"properties": map[string]any{"agentHostingConfigurations": []any{map[string]any{
					"hostingType": "Invalid", "clusterResourceId": clusterA,
				}}},
			}}, nil)},
			wantSub: "sub-a", wantErr: "actual Foundry hostingType must be ManagedCluster",
		},
		{
			name: "output selects known non-BYO account B without setting BYO account or falling back to A",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				}}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
					"properties": map[string]any{"allowProjectManagement": true, "networkInjections": subnet(subnetB)},
				}}, nil),
			},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "account-b"},
			},
			wantSub:            "sub-a",
			wantClusterMissing: "has no ManagedCluster agentHostingConfigurations",
			wantSubnetMissing:  "has no ManagedCluster agentHostingConfigurations",
		},
		{
			name: "unresolved output with competing unresolved BYO account does not guess account A",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				},
				"accountB": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
					"properties": map[string]any{"agentHostingConfigurations": []any{"[parameters('unknownEntry')]"}},
				},
			}, nil)},
			outputs: map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "[parameters('dyn')]"},
			},
			wantSub:            "sub-a",
			wantClusterMissing: "unknownEntry",
			wantSubnetMissing:  "dyn",
		},
		{
			name: "unresolved deployment copy count prevents single-account fallback while model copy does not",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				}}, nil),
				"modLoop": map[string]any{
					"copy": map[string]any{"name": "modLoop", "count": "[parameters('runtimeCount')]"},
					"type": "Microsoft.Resources/deployments", "name": "loop-deploy",
					"properties": map[string]any{
						"expressionEvaluationOptions": map[string]any{"scope": "inner"},
						"template": map[string]any{
							"languageVersion": "2.0", "resources": map[string]any{},
						},
					},
				},
			},
			wantSub: "sub-a", wantClusterMissing: "resource copy count", wantSubnetMissing: "resource copy count",
		},
		{
			name: "conditional single account stops before binding resources or owning subscriptions",
			resources: map[string]any{"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
				"condition": "[parameters('deployB')]",
				"type":      "Microsoft.CognitiveServices/accounts", "name": "account-b",
				"properties": map[string]any{
					"agentHostingConfigurations": []any{cfgB}, "networkInjections": subnet(subnetB),
				},
			}}, nil)},
			wantSub:            "sub-a",
			wantClusterMissing: "creation condition",
			wantSubnetMissing:  "creation condition",
		},
		{
			name: "inactive conditional child output is ignored instead of selecting old account A",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				}}, map[string]any{
					"AZURE_AI_ACCOUNT_NAME": map[string]any{"condition": false, "type": "string", "value": "account-a"},
				}),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgB}},
				}}, nil),
			},
			outputs: map[string]any{"AZURE_AI_ACCOUNT_NAME": map[string]any{
				"type":  "string",
				"value": "[coalesce(tryGet(reference('modA').outputs, 'AZURE_AI_ACCOUNT_NAME', 'value'), 'account-b')]",
			}},
			wantSub: "sub-b", wantClusterID: clusterB, wantProven: true,
		},
		{
			name: "unresolved conditional child output preserves uncertainty instead of falling through coalesce",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				}}, map[string]any{
					"AZURE_AI_ACCOUNT_NAME": map[string]any{
						"condition": "[parameters('unknownCond')]", "type": "string", "value": "account-a",
					},
				}),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgB}},
				}}, nil),
			},
			outputs: map[string]any{"AZURE_AI_ACCOUNT_NAME": map[string]any{
				"type":  "string",
				"value": "[coalesce(tryGet(reference('modA').outputs, 'AZURE_AI_ACCOUNT_NAME', 'value'), 'account-b')]",
			}},
			wantSub: "sub-a", wantClusterMissing: "unknownCond", wantSubnetMissing: "unknownCond",
		},
		{
			name: "selected account with known ManagedCluster and unresolved sibling entry keeps account network",
			resources: map[string]any{"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": map[string]any{
				"type": "Microsoft.CognitiveServices/accounts", "name": "account-b",
				"properties": map[string]any{
					"agentHostingConfigurations": []any{cfgB, "[parameters('extraHosting')]"},
					"networkInjections":          subnet(subnetB),
				},
			}}, nil)},
			wantSub: "sub-b", wantClusterMissing: "extraHosting", wantSubnetID: subnetB,
		},
		{
			name: "unambiguous single account with unrelated model copy loop records singleAccountHosting",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": map[string]any{
					"type": "Microsoft.CognitiveServices/accounts", "name": "account-a",
					"properties": map[string]any{"agentHostingConfigurations": []any{cfgA}},
				},
				"models": map[string]any{
					"copy": map[string]any{"name": "models", "count": "[parameters('runtimeModels')]"},
					"type": "Microsoft.CognitiveServices/accounts/deployments", "name": "account-a/gpt",
				},
			}, nil)},
			wantSub: "sub-a", wantClusterID: clusterA, wantSingle: true, wantProven: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &templatePlanResolver{
				scopes: scopes, functions: map[string]any{},
				references: map[*planResource]bool{}, owningSubscriptions: map[string]bool{"sub-a": true},
			}
			env := resolver.environment(map[string]any{
				"languageVersion": "2.0", "resources": tt.resources, "outputs": tt.outputs,
			}, nil, scopes["sub-a/"], "compiled template", "outer")
			env.function = func(name string, _ []string) any {
				t.Fatalf("unexpected Bicep function evaluation for %s", name)
				return nil
			}
			resolver.walk(env, "", 0)
			plan := onboarding.Plan{SubscriptionID: "sub-a"}
			resolver.bindResources(&plan, env)
			if resolver.account != nil && resolver.account.env.scope.subscriptionID != "" {
				plan.SubscriptionID = resolver.account.env.scope.subscriptionID
			}
			resolver.bindNetwork(&plan)

			if tt.wantErr != "" {
				require.ErrorContains(t, resolver.err, tt.wantErr)
				return
			}
			require.NoError(t, resolver.err)
			assert.Equal(t, tt.wantSub, plan.SubscriptionID)
			assert.Equal(t, tt.wantSingle, resolver.singleAccountHosting)
			assert.Equal(t, tt.wantProven, resolver.accountHostingProven)
			assert.Equal(t, tt.wantClusterID, plan.Cluster.ID)
			if tt.wantClusterMissing != "" {
				assert.Empty(t, plan.Cluster.ID)
				assert.False(t, plan.Hosting.Supplied)
				assert.False(t, plan.Storage.Supplied)
				assert.NotContains(t, resolver.owningSubscriptions, "sub-b")
				assert.Contains(t, plan.Cluster.Missing, tt.wantClusterMissing)
				assert.NotEmpty(t, plan.Workload.Missing)
			} else {
				assert.Empty(t, plan.Cluster.Missing)
			}
			assert.Equal(t, tt.wantSubnetID, plan.AgentSubnet.ID)
			if tt.wantSubnetMissing != "" {
				assert.Contains(t, plan.AgentSubnet.Missing, tt.wantSubnetMissing)
			} else {
				assert.Empty(t, plan.AgentSubnet.Missing)
			}
		})
	}
}

func TestBYOPlanAccountOutputBicepFunctionFailurePropagates(t *testing.T) {
	scope := planScope{
		subscriptionID: "sub-a", subscriptionARMID: "/subscriptions/sub-a",
		group: "rg-a", groupARMID: "/subscriptions/sub-a/resourceGroups/rg-a", groupExists: true,
	}
	p, _ := offlineBYOProvider(t)
	p.bicepCliInstance = &stubCompiler{buildParamErr: assert.AnError}
	resolver := &templatePlanResolver{
		ctx: t.Context(), provider: p, scopes: map[string]planScope{"sub-a/rg-a": scope},
		functions: map[string]any{}, references: map[*planResource]bool{}, owningSubscriptions: map[string]bool{},
	}
	env := resolver.environment(map[string]any{
		"languageVersion": "2.0",
		"outputs": map[string]any{
			"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": "[uniqueString('broken-bicep')]"},
		},
	}, nil, scope, "compiled template", "outer")
	resolver.walk(env, "", 0)
	var plan onboarding.Plan
	resolver.bindResources(&plan, env)
	require.ErrorIs(t, resolver.err, assert.AnError)
}
