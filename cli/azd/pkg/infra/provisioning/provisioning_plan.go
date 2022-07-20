package provisioning

type ProvisioningPlan struct {
	Parameters map[string]ProvisioningPlanInputParameter
	Outputs    map[string]ProvisioningPlanOutputParameter
}

type ProvisioningPlanInputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type ProvisioningPlanOutputParameter struct {
	Type  string
	Value interface{}
}

func (p *ProvisioningPlanInputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *ProvisioningPlanInputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
