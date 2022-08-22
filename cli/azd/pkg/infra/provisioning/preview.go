package provisioning

type Preview struct {
	Parameters map[string]PreviewInputParameter
	Outputs    map[string]PreviewOutputParameter
}

type PreviewInputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type PreviewOutputParameter struct {
	Type  string
	Value interface{}
}

func (p *PreviewInputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *PreviewInputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
