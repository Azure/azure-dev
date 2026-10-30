// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// MonitoringConnection identifies an effective connection without its credentials.
type MonitoringConnection struct {
	Name   string
	Target string
}

// connectionSelector accumulates the single effective AppInsights destination.
type connectionSelector struct {
	reserved string
	selected *MonitoringConnection
}

func (s *connectionSelector) add(name string, properties *armcognitiveservices.ConnectionPropertiesV2) error {
	isMonitoring, err := validateReusableAppInsightsConnection(name, s.reserved, properties)
	if err != nil || !isMonitoring {
		return err
	}
	if s.selected != nil && !strings.EqualFold(s.selected.Target, *properties.Target) {
		return fmt.Errorf("effective AppInsights connections have conflicting destinations; no connection was changed")
	}
	if s.selected == nil {
		s.selected = &MonitoringConnection{Name: name, Target: *properties.Target}
	}
	return nil
}

// DiscoverConnection resolves effective project/inherited connections without fetching credentials.
func (p *MonitoringResolver) DiscoverConnection(
	ctx context.Context, project *arm.ResourceID, projectExists, accountExists bool,
) (*MonitoringConnection, error) {
	reader := p.reader()
	selector := &connectionSelector{reserved: MonitoringConnectionName(project.Name)}
	projectConnections := map[string]*armcognitiveservices.ConnectionPropertiesV2{}
	if projectExists {
		records, err := reader.ProjectConnections(ctx, project)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if err := selector.add(record.Name, record.Properties); err != nil {
				return nil, err
			}
			projectConnections[strings.ToLower(record.Name)] = record.Properties
		}
	}
	if accountExists {
		records, err := reader.AccountConnections(ctx, project)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if err := selectAccountConnection(selector, record, projectConnections); err != nil {
				return nil, err
			}
		}
	}
	if selector.selected == nil && projectExists {
		exists, err := reader.ProjectConnectionExists(ctx, project, selector.reserved)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("the system monitoring connection name is already in use")
		}
	}
	return selector.selected, nil
}

func selectAccountConnection(
	selector *connectionSelector,
	record MonitoringConnectionRecord,
	projectConnections map[string]*armcognitiveservices.ConnectionPropertiesV2,
) error {
	name, properties := record.Name, record.Properties
	if properties == nil || properties.Category == nil {
		return fmt.Errorf("account connection properties are unavailable")
	}
	shared := properties.IsSharedToAll != nil && *properties.IsSharedToAll
	if prior := projectConnections[strings.ToLower(name)]; prior != nil && !shared {
		// ARM mirrors project entries at account scope with different IDs.
		sameTarget := properties.Target == nil && prior.Target == nil
		if properties.Target != nil && prior.Target != nil {
			sameTarget = strings.EqualFold(*properties.Target, *prior.Target)
		}
		if !sameTarget || !strings.EqualFold(string(*properties.Category), string(*prior.Category)) {
			return fmt.Errorf("project and account connection identities are ambiguous")
		}
		return nil
	}
	if !shared {
		if strings.EqualFold(name, selector.reserved) {
			return fmt.Errorf("the system monitoring name is protected by another account connection")
		}
		if strings.EqualFold(string(*properties.Category), "AppInsights") &&
			(properties.IsSharedToAll == nil || len(properties.SharedUserList) > 0) {
			return fmt.Errorf("restricted account monitoring sharing cannot be resolved safely")
		}
		// An unshared connection belonging to a sibling project is not inherited.
		return nil
	}
	return selector.add(name, properties)
}
