package provisioning

type Plan struct {
	Parameters map[string]PlanInputParameter
	Outputs    map[string]PlanOutputParameter
}

type PlanInputParameter struct {
	Type         string
	DefaultValue interface{}
	Value        interface{}
}

type PlanOutputParameter struct {
	Type  string
	Value interface{}
}

func (p *PlanInputParameter) HasValue() bool {
	return p.Value != nil
}

func (p *PlanInputParameter) HasDefaultValue() bool {
	return p.DefaultValue != nil
}
