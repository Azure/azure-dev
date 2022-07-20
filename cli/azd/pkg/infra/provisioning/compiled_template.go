package provisioning

type CompiledTemplate struct {
	Parameters map[string]CompiledTemplateParameter
	Outputs    map[string]CompiledTemplateOutputParameter
}

type CompiledTemplateParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type CompiledTemplateOutputParameter struct {
	Type  string
	Value interface{}
}

func (p *CompiledTemplateParameter) HasValue() bool {
	return p.Value != nil
}

func (p *CompiledTemplateParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
