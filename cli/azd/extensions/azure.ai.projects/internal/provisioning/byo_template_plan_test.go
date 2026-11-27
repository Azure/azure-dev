// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"maps"
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
	armID := func(sub, rg, provider, name string) string {
		return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/" + provider + "/" + name
	}
	clusterA := armID("sub-a", "rg-a", "Microsoft.ContainerService/managedClusters", "aks-a")
	clusterB := armID("sub-b", "rg-b", "Microsoft.ContainerService/managedClusters", "aks-b")
	storageA := armID("sub-a", "rg-a", "Microsoft.Storage/storageAccounts", "storea")
	hostingA := armID("sub-a", "rg-a", "Microsoft.ManagedIdentity/userAssignedIdentities", "hosta")
	workloadA := armID("sub-a", "rg-a", "Microsoft.ManagedIdentity/userAssignedIdentities", "worka")
	subnetA := armID("net-sub", "net-rg", "Microsoft.Network/virtualNetworks", "net/subnets/a")
	subnetB := armID("net-sub", "net-rg", "Microsoft.Network/virtualNetworks", "net/subnets/b")
	mkScope := func(sub, rg string) planScope {
		s := planScope{subscriptionID: sub, subscriptionARMID: "/subscriptions/" + sub}
		if rg != "" {
			s.group, s.groupARMID, s.groupExists = rg, s.subscriptionARMID+"/resourceGroups/"+rg, true
		}
		return s
	}
	scopes := map[string]planScope{
		"sub-a/": mkScope("sub-a", ""), "sub-a/rg-a": mkScope("sub-a", "rg-a"),
		"sub-b/": mkScope("sub-b", ""), "sub-b/rg-b": mkScope("sub-b", "rg-b"),
	}
	cfgA := map[string]any{
		"hostingType": "ManagedCluster", "clusterResourceId": clusterA,
		"storageAccountResourceId": storageA, "hostingManagementIdentityResourceId": hostingA,
		"workloadIdentityResourceId": workloadA,
	}
	cfgB := map[string]any{
		"hostingType": "ManagedCluster", "clusterResourceId": clusterB,
		"storageAccountResourceId": armID("sub-b", "rg-b", "Microsoft.Storage/storageAccounts", "storeb"),
		"hostingManagementIdentityResourceId": armID(
			"sub-b", "rg-b", "Microsoft.ManagedIdentity/userAssignedIdentities", "hostb"),
		"workloadIdentityResourceId": armID(
			"sub-b", "rg-b", "Microsoft.ManagedIdentity/userAssignedIdentities", "workb"),
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
	withFields := func(base, extra map[string]any) map[string]any {
		maps.Copy(base, extra)
		return base
	}
	acct := func(name string, props any) map[string]any {
		return map[string]any{"type": "Microsoft.CognitiveServices/accounts", "name": name, "properties": props}
	}
	byo := func(name string, cfg any, net ...string) map[string]any {
		props := map[string]any{"agentHostingConfigurations": []any{cfg}}
		if len(net) > 0 {
			props["networkInjections"] = subnet(net[0])
		}
		return acct(name, props)
	}
	outName := func(val any) map[string]any {
		return map[string]any{"AZURE_AI_ACCOUNT_NAME": map[string]any{"type": "string", "value": val}}
	}
	twoMods := func(outA map[string]any) map[string]any {
		return map[string]any{
			"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA)}, outA),
			"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": byo("account-b", cfgB)}, nil),
		}
	}

	tests := []struct {
		name                                       string
		resources, outputs                         map[string]any
		wantSub, wantClusterID, wantClusterMissing string
		wantSubnetID, wantSubnetMissing, wantErr   string
		wantSingle, wantProven                     bool
	}{
		{
			name: "single BYO account with dynamic property key and plain or inactive bystanders",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"activeBYO": acct("account-a", map[string]any{
					"[concat('agentHosting', 'Configurations')]": []any{cfgA},
					"[concat('network', 'Injections')]":          subnet(subnetA),
				}),
				"plain":  acct("[uniqueString('unused-plain-name')]", map[string]any{"allowProjectManagement": true}),
				"nonAKS": byo("non-aks", map[string]any{"hostingType": "None"}),
				"inactive": withFields(acct("inactive-b", "[uniqueString('unused-inactive')]"), map[string]any{
					"condition": false, "copy": map[string]any{"name": "inactive", "count": "[parameters('n')]"},
				}),
			}, map[string]any{"UNUSED": map[string]any{"type": "string", "value": "[uniqueString('unused-child')]"}})},
			outputs: withFields(outName(""), map[string]any{
				"UNUSED_ROOT": map[string]any{"type": "string", "value": "[uniqueString('unused-root')]"},
			}),
			wantSub: "sub-a", wantClusterID: clusterA, wantSubnetID: subnetA, wantProven: true,
		},
		{
			name: "genuinely single BYO account with cross-module existing alias and account-free module loop",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": byo("account-a", cfgA, subnetA),
					"models": map[string]any{
						"copy": map[string]any{"name": "models", "count": "[parameters('runtimeModels')]"},
						"type": "Microsoft.CognitiveServices/accounts/deployments", "name": "account-a/gpt",
					},
				}, nil),
				"modTools": mod("mod-tools", "sub-b", "rg-b", map[string]any{
					"existingAlias": map[string]any{
						"existing": true, "type": "Microsoft.CognitiveServices/accounts",
						"subscriptionId": "sub-a", "resourceGroup": "rg-a", "name": "account-a",
					},
				}, nil),
				"diagLoop": withFields(mod("diag", "sub-a", "rg-a", map[string]any{
					"grant": map[string]any{"type": "Microsoft.Authorization/roleAssignments", "name": "ra-1"},
				}, nil), map[string]any{"copy": map[string]any{"name": "diagLoop", "count": "[parameters('runtimeDiag')]"}}),
			},
			outputs: outName("[parameters('dynamicAccount')]"),
			wantSub: "sub-a", wantClusterID: clusterA, wantSubnetID: subnetA, wantSingle: true, wantProven: true,
		},
		{
			name: "same-name cross-group existing reference in same module is not collapsed into deployed account",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", cfgA, subnetA),
				"existingOtherRG": map[string]any{
					"existing": true, "type": "Microsoft.CognitiveServices/accounts",
					"resourceGroup": "rg-b", "name": "account-a",
				},
			}, nil)},
			outputs: outName("account-a"), wantSub: "sub-a",
			wantClusterMissing: "matched multiple or ambiguous", wantSubnetMissing: "matched multiple or ambiguous",
		},
		{
			name: "unresolved output with BYO and known non-BYO account preserves selector uncertainty",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", cfgA),
				"accountB": acct("account-b", map[string]any{"allowProjectManagement": true}),
			}, nil)},
			outputs: outName("[parameters('dynamicAccount')]"),
			wantSub: "sub-a", wantClusterMissing: "dynamicAccount", wantSubnetMissing: "dynamicAccount",
		},
		{
			name: "output selects unresolved account B without evaluating bystander A properties",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": acct("account-a", "[createObject('agentHostingConfigurations', "+
						"createArray(createObject('hostingType', 'ManagedCluster', "+
						"'clusterResourceId', uniqueString('bystander-must-not-run'))))]"),
				}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
					"accountB": acct("account-b", map[string]any{
						"agentHostingConfigurations": "[parameters('runtimeHostingB')]",
						"networkInjections":          subnet(subnetB),
					}),
				}, outName("ACCOUNT-B")),
			},
			outputs: withFields(outName(
				"[if(empty(reference('modB').outputs), '', reference('modB').outputs.AZURE_AI_ACCOUNT_NAME.value)]"),
				map[string]any{"UNUSED_ROOT": map[string]any{"type": "string", "value": "[uniqueString('unused-root')]"}}),
			wantSub: "sub-b", wantClusterMissing: "runtimeHostingB", wantSubnetID: subnetB,
		},
		{
			name: "output selects account B hidden in unexpanded linked module",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA, subnetA)}, nil),
				"linkedB": map[string]any{
					"type": "Microsoft.Resources/deployments", "name": "linked-b",
					"properties": map[string]any{"templateLink": map[string]any{"uri": "https://example.test/b.json"}},
				},
			},
			outputs: outName("account-b"),
			wantSub: "sub-a", wantClusterMissing: "did not match a local Foundry account", wantSubnetMissing: "linkedB",
		},
		{
			name: "output selects resolved account B and ignores bystander invalid hostingType or unknown properties",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
					"accountA": byo("account-a", map[string]any{"hostingType": "Invalid", "clusterResourceId": clusterA}),
					"accountC": acct("account-c", "[parameters('unknownPropsC')]"),
				}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": byo("account-b", cfgB, subnetB)}, nil),
			},
			outputs: outName("account-b"), wantSub: "sub-b", wantClusterID: clusterB,
			wantSubnetID: subnetB, wantProven: true,
		},
		{
			name: "output with unknown-name plain bystander retains ambiguity across resource groups",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA)}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
					"plainUnknownName": acct("[parameters('dynPlainName')]", map[string]any{"allowProjectManagement": true}),
				}, nil),
			},
			outputs: outName("account-a"),
			wantSub: "sub-a", wantClusterMissing: "dynPlainName", wantSubnetMissing: "dynPlainName",
		},
		{
			name: "plain bystander alongside sole BYO account with opaque workload keeps singleAccountHosting",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", map[string]any{
					"hostingType": "ManagedCluster", "clusterResourceId": clusterA, "storageAccountResourceId": storageA,
					"hostingManagementIdentityResourceId": hostingA,
					"workloadIdentityResourceId":          "[parameters('opaqueWorkload')]",
				}),
				"speechCog": acct("speech-cog", map[string]any{"allowProjectManagement": false}),
			}, nil)},
			outputs: outName("account-a"), wantSub: "sub-a", wantClusterID: clusterA, wantSingle: true, wantProven: true,
		},
		{
			name: "selected single account with invalid hostingType reports error",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", map[string]any{"hostingType": "Invalid", "clusterResourceId": clusterA}),
			}, nil)},
			wantSub: "sub-a", wantErr: "actual Foundry hostingType must be ManagedCluster",
		},
		{
			name: "output selects known non-BYO account B without setting BYO account or falling back to A",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA)}, nil),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{"accountB": acct("account-b", map[string]any{
					"allowProjectManagement": true, "networkInjections": subnet(subnetB),
				})}, nil),
			},
			outputs: outName("account-b"), wantSub: "sub-a",
			wantClusterMissing: "has no ManagedCluster agentHostingConfigurations",
			wantSubnetMissing:  "has no ManagedCluster agentHostingConfigurations",
		},
		{
			name: "unresolved output with competing unresolved BYO account does not guess account A",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", cfgA), "accountB": byo("account-b", "[parameters('unknownEntry')]"),
			}, nil)},
			outputs: outName("[parameters('dyn')]"),
			wantSub: "sub-a", wantClusterMissing: "unknownEntry", wantSubnetMissing: "dyn",
		},
		{
			name: "property-level copy on agentHostingConfigurations is treated as unresolved rather than non-hosting",
			resources: map[string]any{"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{
				"accountA": byo("account-a", cfgA),
				"accountB": acct("account-b", map[string]any{"copy": []any{map[string]any{
					"name": "agentHostingConfigurations", "count": "[parameters('cfgCount')]", "input": cfgB,
				}}}),
			}, nil)},
			wantSub: "sub-a", wantClusterMissing: "property copy loop", wantSubnetMissing: "property copy loop",
		},
		{
			name: "filtered copy loop above limit or unknown count prevents single-account fallback",
			resources: map[string]any{
				"modA": mod("mod-a", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA)}, nil),
				"modLoop": withFields(mod("loop-deploy", "sub-a", "rg-a", map[string]any{
					"loopAccount": acct("account-loop", nil),
				}, nil), map[string]any{
					"condition": "[equals(copyIndex(), 1)]",
					"copy":      map[string]any{"name": "modLoop", "count": 200.0},
				}),
			},
			wantSub: "sub-a", wantClusterMissing: "resource copy count", wantSubnetMissing: "resource copy count",
		},
		{
			name: "conditional single account stops before binding resources or owning subscriptions",
			resources: map[string]any{"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
				"accountB": withFields(byo("account-b", cfgB, subnetB),
					map[string]any{"condition": "[parameters('deployB')]"}),
			}, nil)},
			wantSub: "sub-a", wantClusterMissing: "creation condition", wantSubnetMissing: "creation condition",
		},
		{
			name: "inactive conditional child output is ignored instead of selecting old account A",
			resources: twoMods(map[string]any{
				"AZURE_AI_ACCOUNT_NAME": map[string]any{"condition": false, "type": "string", "value": "account-a"},
			}),
			outputs: outName("[coalesce(tryGet(reference('modA').outputs, 'AZURE_AI_ACCOUNT_NAME', 'value'), 'account-b')]"),
			wantSub: "sub-b", wantClusterID: clusterB, wantProven: true,
		},
		{
			name: "unresolved conditional child output preserves uncertainty instead of falling through coalesce",
			resources: twoMods(map[string]any{"AZURE_AI_ACCOUNT_NAME": map[string]any{
				"condition": "[parameters('unknownCond')]", "type": "string", "value": "account-a",
			}}),
			outputs: outName("[coalesce(tryGet(reference('modA').outputs, 'AZURE_AI_ACCOUNT_NAME', 'value'), 'account-b')]"),
			wantSub: "sub-a", wantClusterMissing: "unknownCond", wantSubnetMissing: "unknownCond",
		},
		{
			name: "unresolved conditional child output preserves uncertainty in empty(outputs) selector",
			resources: twoMods(map[string]any{"CONDITIONAL_ACCOUNT": map[string]any{
				"condition": "[parameters('unknownCond')]", "type": "string", "value": "account-a",
			}}),
			outputs: outName("[if(empty(tryGet(reference('modA'), 'outputs')), 'account-b', 'account-a')]"),
			wantSub: "sub-a", wantClusterMissing: "unknownCond", wantSubnetMissing: "unknownCond",
		},
		{
			name: "unconditional sibling output and shallowMerge collision prove presence across multiple accounts",
			resources: twoMods(map[string]any{"MAYBE": map[string]any{
				"condition": "[parameters('unknownCond')]", "type": "string", "value": "x",
			}}),
			outputs: outName("[if(and(not(empty(shallowMerge(createArray(createObject('maybe', 'v'), " +
				"tryGet(reference('modA'), 'outputs'))))), equals(length(shallowMerge(createArray(" +
				"createObject('maybe', 'v'), tryGet(reference('modA'), 'outputs')))), 1)), 'account-b', 'account-a')]"),
			wantSub: "sub-b", wantClusterID: clusterB, wantProven: true,
		},
		{
			name: "direct reference to inactive module does not walk or select inactive account",
			resources: map[string]any{
				"modOff": withFields(mod("mod-off", "sub-a", "rg-a", map[string]any{"accountA": byo("account-a", cfgA)},
					map[string]any{"NAME": map[string]any{"type": "string", "value": "account-a"}}),
					map[string]any{"condition": false}),
				"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
					"accountB": byo("account-b", cfgB),
					"plainC":   acct("account-c", map[string]any{"allowProjectManagement": true}),
				}, nil),
			},
			outputs: outName("[reference('modOff').outputs.NAME.value]"),
			wantSub: "sub-a", wantClusterMissing: "is inactive", wantSubnetMissing: "is inactive",
		},
		{
			name: "selected account with known ManagedCluster and unresolved sibling entry keeps account network",
			resources: map[string]any{"modB": mod("mod-b", "sub-b", "rg-b", map[string]any{
				"accountB": acct("account-b", map[string]any{
					"agentHostingConfigurations": []any{cfgB, "[parameters('extraHosting')]"},
					"networkInjections":          subnet(subnetB),
				}),
			}, nil)},
			wantSub: "sub-b", wantClusterMissing: "extraHosting", wantSubnetID: subnetB,
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
