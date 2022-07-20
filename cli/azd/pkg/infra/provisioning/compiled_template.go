package provisioning

type CompiledTemplate struct {
	Parameters map[string]CompiledTemplateParameter
	Outputs    []InfraDeploymentOutputParameter
}

type CompiledTemplateParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

func (p *CompiledTemplateParameter) HasValue() bool {
	return p.Value != nil
}

func (p *CompiledTemplateParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
