// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type networkCleanupARM struct {
	*cleanupARM
	births                       map[string]string
	records                      map[string][]any
	pe, nic, group, vnet, subnet string
	zones, links                 []string
	keepNIC                      bool
	omitNetworkOperations        bool
}

func newNetworkCleanupARM(t *testing.T) *networkCleanupARM {
	t.Helper()
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	base := "/subscriptions/" + f.base.plan.SubscriptionID + "/resourceGroups/" + f.base.plan.ResourceGroup
	n := &networkCleanupARM{cleanupARM: f, births: map[string]string{}, records: map[string][]any{}}
	n.pe = networkPrivateEndpointID(&PreparationState{
		SubscriptionID: f.base.plan.SubscriptionID, ResourceGroup: f.base.plan.ResourceGroup, Owner: f.base.plan.Owner,
	})
	n.group = n.pe + "/privateDnsZoneGroups/" + PlannedFoundryAccount(f.base.plan).Name + "-dns-group"
	n.nic = base + "/providers/" + privateNICType + "/service-owned-interface"
	n.vnet = base + "/providers/Microsoft.Network/virtualNetworks/provided"
	n.subnet = n.vnet + "/subnets/provided-pe"
	put := func(id, kind, location string, properties map[string]any) {
		f.base.resources[strings.ToLower(id)] = map[string]any{
			"id": id, "name": id[strings.LastIndex(id, "/")+1:], "type": kind,
			"location": location, "properties": properties,
		}
		n.births[strings.ToLower(id)] = "2026-10-26T00:50:00Z"
	}
	put(n.pe, privateEndpointType, f.spec.Location, map[string]any{
		"provisioningState": "Succeeded", "resourceGuid": "00000000-0000-0000-0000-000000000077",
		"subnet": map[string]any{"id": n.subnet}, "networkInterfaces": []any{map[string]any{"id": n.nic}},
		"privateLinkServiceConnections": []any{map[string]any{
			"properties": map[string]any{"privateLinkServiceId": f.spec.AccountID, "groupIds": []any{"account"}},
		}},
	})
	var ips []any
	for _, ip := range []string{"10.225.1.6", "10.225.1.5", "10.225.1.4"} {
		ips = append(ips, map[string]any{"properties": map[string]any{
			"privateIPAddress": ip, "subnet": map[string]any{"id": n.subnet},
		}})
	}
	put(n.nic, privateNICType, f.spec.Location, map[string]any{
		"provisioningState": "Succeeded", "privateEndpoint": map[string]any{"id": n.pe}, "ipConfigurations": ips,
	})
	f.base.resources[strings.ToLower(n.nic)]["managedBy"] = n.pe
	var configs []any
	for index, zone := range []string{
		"privatelink.services.ai.azure.com", "privatelink.openai.azure.com", "privatelink.cognitiveservices.azure.com",
	} {
		id := base + "/providers/" + privateZoneType + "/" + zone
		link := id + "/virtualNetworkLinks/exact-created-link"
		n.zones, n.links = append(n.zones, id), append(n.links, link)
		put(id, privateZoneType, "global", map[string]any{"provisioningState": "Succeeded"})
		put(link, privateLinkType, "global", map[string]any{
			"provisioningState": "Succeeded", "registrationEnabled": false, "virtualNetwork": map[string]any{"id": n.vnet},
		})
		account := PlannedFoundryAccount(f.base.plan).Name
		address := []string{"10.225.1.6", "10.225.1.5", "10.225.1.4"}[index]
		configs = append(configs, map[string]any{"properties": map[string]any{
			"privateDnsZoneId": id,
			"recordSets":       []any{map[string]any{"recordType": "A", "recordSetName": account, "ipAddresses": []any{address}}},
		}})
		n.records[strings.ToLower(id)] = []any{
			map[string]any{"id": id + "/SOA/@", "name": "@", "type": privateZoneType + "/SOA", "properties": map[string]any{}},
			map[string]any{"id": id + "/A/" + account, "name": account, "type": privateZoneType + "/A",
				"properties": map[string]any{
					"aRecords": []any{map[string]any{"ipv4Address": address}},
					"metadata": map[string]any{
						"creator": "created by private endpoint " + account +
							"-private-endpoint with resource guid 00000000-0000-0000-0000-000000000077",
					},
				}},
		}
	}
	put(n.group, privateZoneGroupType, "", map[string]any{
		"provisioningState": "Succeeded", "privateDnsZoneConfigs": configs,
	})
	delete(n.births, strings.ToLower(n.group))
	for _, root := range f.roots {
		p := cleanupObject(t, root["properties"])
		parameters := cleanupObject(t, p["parameters"])
		for name, value := range map[string]any{
			"enableNetworkIsolation": true, "vnetId": n.vnet, "peSubnetName": "provided-pe",
			"dnsZonesResourceGroup": "", "dnsZonesSubscription": "",
		} {
			parameters[name] = map[string]any{"value": value}
		}
		p["timestamp"], p["duration"] = "2026-10-26T00:52:00Z", "PT7M0S"
		var outputs []any
		for id := range n.births {
			if id != strings.ToLower(n.nic) {
				outputs = append(outputs, map[string]any{"id": id})
			}
		}
		outputs = append(outputs, map[string]any{"id": n.group})
		p["outputResources"] = outputs
	}
	transport := f.options.Transport
	f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
		path := strings.ToLower(request.URL.Path)
		respond := func(value any) (*http.Response, error) {
			raw, err := json.Marshal(value)
			require.NoError(t, err)
			return contractARMResponse(request, 200, string(raw)), nil
		}
		if request.Method == http.MethodGet {
			if strings.HasSuffix(path, "/operations") && strings.Contains(path, "/providers/microsoft.resources/") {
				response, err := transport.Do(request)
				require.NoError(t, err)
				var page map[string]any
				require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
				require.NoError(t, response.Body.Close())
				values, ok := page["value"].([]any)
				require.True(t, ok)
				ids := append(append([]string{n.pe, n.group}, n.zones...), n.links...)
				if n.omitNetworkOperations {
					ids = nil
				}
				for _, id := range ids {
					kind := privateEndpointType
					switch {
					case strings.Contains(id, "/privateDnsZoneGroups/"):
						kind = privateZoneGroupType
					case strings.Contains(id, "/virtualNetworkLinks/"):
						kind = privateLinkType
					case strings.Contains(id, "/privateDnsZones/"):
						kind = privateZoneType
					}
					values = append(values, map[string]any{
						"id": strings.TrimSuffix(request.URL.Path, "/operations") + "/operations/network-" + id[strings.LastIndex(id, "/")+1:],
						"properties": map[string]any{
							"provisioningOperation": "Create", "provisioningState": "Succeeded",
							"targetResource": map[string]any{"id": id, "resourceType": kind},
						},
					})
				}
				page["value"] = values
				return respond(page)
			}
			if path == strings.ToLower(base)+"/resources" {
				require.Contains(t, request.URL.Query().Get("$expand"), "createdTime")
				var values []any
				for id, birth := range n.births {
					if actual := f.base.resources[id]; actual != nil {
						copy := map[string]any{"id": id, "createdTime": birth, "changedTime": "2026-10-26T02:03:00Z"}
						values = append(values, copy)
					}
				}
				return respond(map[string]any{"value": values})
			}
			if strings.HasSuffix(path, "/privateendpointconnections") {
				if f.base.resources[strings.ToLower(n.pe)] != nil {
					return respond(map[string]any{"value": []any{map[string]any{
						"id":         f.spec.AccountID + "/privateEndpointConnections/owned-service-link",
						"properties": map[string]any{"privateEndpoint": map[string]any{"id": n.pe}},
					}}})
				}
				return respond(map[string]any{"value": []any{}})
			}
			if strings.EqualFold(path, n.pe+"/privateDnsZoneGroups") ||
				strings.HasSuffix(path, "/virtualnetworklinks") {
				var values []any
				for id, actual := range f.base.resources {
					if strings.HasPrefix(id, path+"/") && !strings.Contains(strings.TrimPrefix(id, path+"/"), "/") {
						values = append(values, actual)
					}
				}
				return respond(map[string]any{"value": values})
			}
			if before, ok := strings.CutSuffix(path, "/all"); ok {
				return respond(map[string]any{"value": n.records[before]})
			}
		}
		response, err := transport.Do(request)
		if err == nil && request.Method == http.MethodDelete && response.StatusCode < 300 {
			if strings.EqualFold(path, n.group) {
				for zone, records := range n.records {
					n.records[zone] = records[:1]
				}
			}
			if strings.EqualFold(path, n.pe) && !n.keepNIC {
				delete(f.base.resources, strings.ToLower(n.nic))
			}
		}
		return response, err
	}).Transport
	f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
	return n
}

func TestOwnedCleanupNetworkBirthAndExactTopology(t *testing.T) {
	f := newNetworkCleanupARM(t)
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	require.NotNil(t, state.Foundry.Network)
	require.Len(t, state.Foundry.Network.Resources, 9)
	for id, resource := range state.Foundry.Network.Resources {
		assert.True(t, resource.Deleted, id)
		assert.NotContains(t, f.base.resources, id)
		if resource.Type != privateZoneGroupType {
			assert.False(t, resource.CreatedTime.IsZero(), id)
			assert.True(t, strings.EqualFold(id, resource.BirthResourceID), id)
		} else {
			assert.True(t, strings.EqualFold(f.pe, resource.BirthResourceID),
				"zone groups use parent endpoint birth, never an invented independent timestamp")
		}
	}
	assert.NotContains(t, f.deletes, strings.ToLower(f.nic), "RP-owned NIC must never receive a direct DELETE")
	assert.NotContains(t, f.deletes, f.vnet)
	assert.NotContains(t, f.deletes, f.subnet)
	assert.True(t, state.CleanupComplete)
}

func TestOwnedCleanupNetworkRefusesUnknownOwnershipBeforeAnyDelete(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*networkCleanupARM)
		want   string
	}{
		{"preexisting-zone", func(f *networkCleanupARM) {
			f.births[strings.ToLower(f.zones[0])] = "2026-10-25T00:00:00Z"
		}, "outside"},
		{"missing-birth", func(f *networkCleanupARM) { delete(f.births, strings.ToLower(f.pe)) }, "createdTime"},
		{"missing-operation-evidence", func(f *networkCleanupARM) { f.omitNetworkOperations = true }, "Create evidence"},
		{"missing-root-output-binding", func(f *networkCleanupARM) {
			for _, root := range f.roots {
				cleanupObject(t, root["properties"])["outputResources"] = nil
			}
		}, "output-resource binding"},
		{"changed-subnet", func(f *networkCleanupARM) {
			cleanupObject(t, f.base.resources[strings.ToLower(f.pe)]["properties"])["subnet"] = map[string]any{"id": f.vnet + "/subnets/foreign"}
		}, "subnet"},
		{"foreign-service", func(f *networkCleanupARM) {
			p := cleanupObject(t, f.base.resources[strings.ToLower(f.pe)]["properties"])
			p["privateLinkServiceConnections"] = []any{map[string]any{
				"properties": map[string]any{"privateLinkServiceId": f.spec.AccountID + "-foreign", "groupIds": []any{"account"}},
			}}
		}, "owned Foundry"},
		{"foreign-NIC", func(f *networkCleanupARM) {
			f.base.resources[strings.ToLower(f.nic)]["managedBy"] = "/foreign"
		}, "managed"},
		{"foreign-link", func(f *networkCleanupARM) {
			id := f.zones[0] + "/virtualNetworkLinks/customer"
			f.base.resources[strings.ToLower(id)] = map[string]any{"id": id}
		}, "untracked"},
		{"foreign-record", func(f *networkCleanupARM) {
			key := strings.ToLower(f.zones[0])
			f.records[key] = append(f.records[key], map[string]any{
				"id": f.zones[0] + "/TXT/customer", "name": "customer", "type": privateZoneType + "/TXT", "properties": map[string]any{},
			})
		}, "untracked record"},
		{"changed-DNS-address", func(f *networkCleanupARM) {
			record := cleanupObject(t, f.records[strings.ToLower(f.zones[0])][1])
			cleanupObject(t, record["properties"])["aRecords"] = []any{map[string]any{"ipv4Address": "10.99.0.9"}}
		}, "actual owned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNetworkCleanupARM(t)
			test.change(f)
			require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), test.want)
			assert.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupNetworkRetainsDependenciesAndResumes(t *testing.T) {
	for _, stage := range []string{"zone-group", "endpoint", "link", "zone", "NIC"} {
		t.Run(stage, func(t *testing.T) {
			f := newNetworkCleanupARM(t)
			switch stage {
			case "zone-group":
				f.failDelete = f.group
			case "endpoint":
				f.failDelete = f.pe
			case "link":
				f.failDelete = f.links[0]
			case "zone":
				f.failDelete = f.zones[0]
			case "NIC":
				f.keepNIC = true
			}
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			assert.NotContains(t, f.deletes, f.spec.AccountID)
			state, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			assert.False(t, state.CleanupComplete)
			f.failDelete = ""
			if stage == "NIC" {
				delete(f.base.resources, strings.ToLower(f.nic))
			}
			require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
		})
	}
}

func TestNetworkBirthUsesCreationNotChangeAndTerminalWindow(t *testing.T) {
	f := newNetworkCleanupARM(t)
	var root map[string]any
	for _, candidate := range f.roots {
		root = candidate
	}
	raw, err := json.Marshal(root)
	require.NoError(t, err)
	var decoded struct {
		Properties *armresources.DeploymentPropertiesExtended `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	window, err := networkWindow(decoded.Properties)
	require.NoError(t, err)
	assert.Equal(t, "2026-10-26T00:45:00Z", window.start.Format(time.RFC3339))
	assert.Equal(t, "2026-10-26T00:52:00Z", window.end.Format(time.RFC3339))
}

func TestOwnedCleanupRetainsSuppliedDNSZonesAndLinks(t *testing.T) {
	f := newNetworkCleanupARM(t)
	for _, root := range f.roots {
		parameters := cleanupObject(t, cleanupObject(t, root["properties"])["parameters"])
		parameters["dnsZonesResourceGroup"] = map[string]any{"value": f.base.plan.ResourceGroup}
	}
	transport := f.options.Transport
	f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
		response, err := transport.Do(request)
		if err != nil || !strings.HasSuffix(request.URL.Path, "/operations") {
			return response, err
		}
		var page map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
		require.NoError(t, response.Body.Close())
		values := []any{}
		for _, operation := range page["value"].([]any) {
			p := cleanupObject(t, cleanupObject(t, operation)["properties"])
			target := cleanupObject(t, p["targetResource"])
			if kind := target["resourceType"]; kind != privateZoneType && kind != privateLinkType {
				values = append(values, operation)
			}
		}
		page["value"] = values
		raw, err := json.Marshal(page)
		require.NoError(t, err)
		return contractARMResponse(request, 200, string(raw)), nil
	}).Transport
	f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
	for _, id := range append(f.zones, f.links...) {
		assert.Contains(t, f.base.resources, strings.ToLower(id))
		assert.NotContains(t, f.deletes, strings.ToLower(id))
	}
}

func TestOwnedCleanupNetworkRejectsRecreationAndResumesJournalFailure(t *testing.T) {
	for _, scenario := range []string{"changed-birth", "recreated-group", "journal-failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkCleanupARM(t)
			original := f.base.resources[strings.ToLower(f.group)]
			if scenario == "journal-failure" {
				f.failJournalOnDelete = f.group
			} else {
				f.failDelete = f.pe
			}
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			f.failDelete, f.failJournalOnDelete, f.base.journal.fail = "", "", false
			before := len(f.deletes)
			switch scenario {
			case "changed-birth":
				f.births[strings.ToLower(f.zones[0])] = "2026-10-26T00:50:01Z"
				require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "creation identity changed")
			case "recreated-group":
				f.base.resources[strings.ToLower(f.group)] = original
				require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "recreated")
			default:
				require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
				return
			}
			assert.Len(t, f.deletes, before)
		})
	}
}

func TestOwnedCleanupNetworkIncompletePagedInventoryStopsBeforeDelete(t *testing.T) {
	for _, inventory := range []string{"resources", "privateDnsZoneGroups", "virtualNetworkLinks", "ALL"} {
		t.Run(inventory, func(t *testing.T) {
			f := newNetworkCleanupARM(t)
			transport := f.options.Transport
			f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(strings.ToLower(request.URL.Path), "/"+strings.ToLower(inventory)) {
					return transport.Do(request)
				}
				if request.URL.Query().Get("page") == "2" {
					return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				response, err := transport.Do(request)
				require.NoError(t, err)
				var page map[string]any
				require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
				require.NoError(t, response.Body.Close())
				page["nextLink"] = request.URL.String() + "&page=2"
				raw, err := json.Marshal(page)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			}).Transport
			f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
			require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "AuthorizationFailed")
			assert.Empty(t, f.deletes)
		})
	}
}
