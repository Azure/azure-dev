// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore ARMID copyindex createarray createobject extensionresourceid
// cspell:ignore resourceid resourceinfo shallowmerge subscriptionresourceid
// cspell:ignore tolower toupper tryget uniquestring

package provisioning

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

type planBindingMask uint8

const (
	planBindingHostingPrincipal planBindingMask = 1 << iota
	planBindingWorkloadPrincipal
)

// This adapter evaluates only the local expressions needed to identify a deployment plan.
// Runtime reference properties and unsupported ARM constructs remain explicit unknown values.
type planUnknown struct {
	reason   string
	bindings planBindingMask
	nonEmpty bool
}

type planPartialID struct {
	kind, scope, missing string
	components           []any
	bindings             planBindingMask
}

func planBindings(values ...any) planBindingMask {
	var mask planBindingMask
	for _, value := range values {
		switch current := value.(type) {
		case planUnknown:
			mask |= current.bindings
		case planPartialID:
			mask |= current.bindings
		case map[string]any:
			for _, field := range current {
				mask |= planBindings(field)
			}
		case []any:
			for _, item := range current {
				mask |= planBindings(item)
			}
		}
	}
	return mask
}

type planExpression struct {
	name     string
	literal  any
	args     []planExpression
	selector *planExpression
	base     *planExpression
}

type planExpressionParser struct {
	text string
	pos  int
}

func (p *planExpressionParser) space() {
	for p.pos < len(p.text) && unicode.IsSpace(rune(p.text[p.pos])) {
		p.pos++
	}
}

func (p *planExpressionParser) consume(c byte) bool {
	p.space()
	if p.pos < len(p.text) && p.text[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *planExpressionParser) identifier() string {
	p.space()
	start := p.pos
	for p.pos < len(p.text) {
		c := rune(p.text[p.pos])
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '$' && c != '#' {
			break
		}
		p.pos++
	}
	return p.text[start:p.pos]
}

func (p *planExpressionParser) expression(depth int) (planExpression, error) {
	if depth > 64 {
		return planExpression{}, fmt.Errorf("ARM expression exceeds the local plan adapter's nesting limit")
	}
	p.space()
	var result planExpression
	if p.consume('\'') {
		var literal strings.Builder
		closed := false
		for p.pos < len(p.text) {
			c := p.text[p.pos]
			p.pos++
			if c == '\'' {
				if p.pos < len(p.text) && p.text[p.pos] == '\'' {
					p.pos++
					literal.WriteByte('\'')
					continue
				}
				closed = true
				break
			}
			literal.WriteByte(c)
		}
		if !closed {
			return result, fmt.Errorf("ARM expression has an unterminated string")
		}
		result.literal = literal.String()
	} else if p.pos < len(p.text) && (p.text[p.pos] == '-' || unicode.IsDigit(rune(p.text[p.pos]))) {
		start := p.pos
		for p.pos < len(p.text) && strings.ContainsRune("-0123456789.", rune(p.text[p.pos])) {
			p.pos++
		}
		value, err := strconv.ParseFloat(p.text[start:p.pos], 64)
		if err != nil {
			return result, fmt.Errorf("ARM expression has an unsupported numeric literal")
		}
		result.literal = value
	} else {
		result.name = p.identifier()
		if result.name == "" || !p.consume('(') {
			return result, fmt.Errorf("ARM expression syntax is not supported by the local plan adapter")
		}
		if !p.consume(')') {
			for {
				arg, err := p.expression(depth + 1)
				if err != nil {
					return result, err
				}
				result.args = append(result.args, arg)
				if p.consume(')') {
					break
				}
				if !p.consume(',') {
					return result, fmt.Errorf("ARM expression has an unsupported argument list")
				}
			}
		}
	}
	for {
		switch {
		case p.consume('.'):
			name := p.identifier()
			if name == "" {
				return result, fmt.Errorf("ARM expression property selection is incomplete")
			}
			base := result
			result = planExpression{base: &base, selector: &planExpression{literal: name}}
		case p.consume('['):
			index, err := p.expression(depth + 1)
			if err != nil || !p.consume(']') {
				return result, fmt.Errorf("ARM expression index selection is unsupported")
			}
			base := result
			result = planExpression{base: &base, selector: &index}
		default:
			return result, nil
		}
	}
}

type planScope struct {
	subscriptionID, subscriptionARMID, group, groupARMID, location string
	groupExists                                                    bool
	missing                                                        string
}

type planEnvironment struct {
	template   map[string]any
	parameters map[string]func() any
	scope      planScope
	path       string
	deployment any
	resources  map[string]*planResource
	variables  map[string]any
	outputs    map[string]any
	active     map[string]bool
	copyIndex  int
	function   func(string, []string) any
	resolver   *templatePlanResolver
	walked     bool
}

func (e *planEnvironment) scanBindings(evals ...func() any) planBindingMask {
	if e.resolver != nil {
		prev := e.resolver.skipFunctions
		e.resolver.skipFunctions = true
		defer func() { e.resolver.skipFunctions = prev }()
	}
	var mask planBindingMask
	for _, fn := range evals {
		mask |= planBindings(fn())
	}
	return mask
}

func planField(value any, key string) any {
	if unknown, ok := value.(planUnknown); ok {
		unknown.nonEmpty = false
		return unknown
	}
	object, ok := value.(map[string]any)
	if !ok {
		return planUnknown{reason: "expected an object when selecting " + key, bindings: planBindings(value)}
	}
	for name, field := range object {
		if strings.EqualFold(name, key) {
			return field
		}
	}
	return planUnknown{reason: "object has no " + key + " property"}
}

func planString(value any) (string, string) {
	if unknown, ok := value.(planUnknown); ok {
		return "", unknown.reason
	}
	if result, ok := value.(string); ok {
		return result, ""
	}
	if partial, ok := value.(planPartialID); ok {
		return "", partial.missing
	}
	return "", "value is not a locally resolved string"
}

func planBool(value any) (bool, string) {
	if unknown, ok := value.(planUnknown); ok {
		return false, unknown.reason
	}
	if result, ok := value.(bool); ok {
		return result, ""
	}
	return false, "condition is not a locally resolved boolean"
}

func planScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, float64:
		return true
	default:
		return false
	}
}

func (e *planEnvironment) value(raw any) any {
	switch value := raw.(type) {
	case string:
		if strings.HasPrefix(value, "[[") {
			return value[1:]
		}
		if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
			return value
		}
		parser := planExpressionParser{text: value[1 : len(value)-1]}
		expression, err := parser.expression(0)
		parser.space()
		if err != nil {
			return planUnknown{reason: err.Error()}
		}
		if parser.pos != len(parser.text) {
			return planUnknown{reason: "ARM expression contains unsupported trailing syntax"}
		}
		return e.evaluate(expression, 0)
	case map[string]any:
		result := make(map[string]any, len(value))
		var keyMissing string
		var mask planBindingMask
		for _, key := range slices.Sorted(maps.Keys(value)) {
			entry := value[key]
			if keyMissing != "" {
				k := key
				mask |= e.scanBindings(
					func() any { return e.value(k) },
					func() any { return e.value(entry) },
				)
				continue
			}
			keyValue := e.value(key)
			name, missing := planString(keyValue)
			if missing != "" {
				keyMissing = missing
				mask |= planBindings(keyValue) | e.scanBindings(func() any { return e.value(entry) })
				continue
			}
			fieldValue := e.value(entry)
			mask |= planBindings(fieldValue)
			result[name] = fieldValue
		}
		if keyMissing != "" {
			return planUnknown{reason: "object key cannot be resolved: " + keyMissing, bindings: mask}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, entry := range value {
			result[i] = e.value(entry)
		}
		return result
	default:
		return value
	}
}

func (e *planEnvironment) variable(name string) any {
	key := strings.ToLower(name)
	if result, found := e.variables[key]; found {
		return result
	}
	if e.active[key] || len(e.active) >= 64 {
		return planUnknown{reason: "variable " + name + " is cyclic or exceeds the local dependency limit"}
	}
	definitions, _ := e.template["variables"].(map[string]any)
	e.active[key] = true
	defer delete(e.active, key)
	var beforeSkipped int
	if e.resolver != nil {
		beforeSkipped = e.resolver.skippedCount
	}
	for variable, raw := range definitions {
		if strings.EqualFold(variable, name) {
			result := e.value(raw)
			if e.resolver == nil || e.resolver.skippedCount == beforeSkipped {
				e.variables[key] = result
			}
			return result
		}
	}
	return planUnknown{reason: "variable " + name + " is not defined in " + e.path}
}

func (e *planEnvironment) parameter(name string) any {
	key := strings.ToLower(name)
	if e.active["parameter:"+key] {
		return planUnknown{reason: "parameter " + name + " has a cyclic default expression"}
	}
	e.active["parameter:"+key] = true
	defer delete(e.active, "parameter:"+key)
	if parameter, found := e.parameters[key]; found {
		return parameter()
	}
	definitions, _ := e.template["parameters"].(map[string]any)
	for parameter, definition := range definitions {
		if strings.EqualFold(parameter, name) {
			if definition, ok := definition.(map[string]any); ok {
				if value, present := definition["defaultValue"]; present {
					return e.value(value)
				}
			}
		}
	}
	return planUnknown{reason: "parameter " + name + " has no resolved value/default in " + e.path}
}

func (e *planEnvironment) evaluate(expression planExpression, depth int) any {
	if depth > 64 {
		return planUnknown{reason: "ARM expression evaluation exceeds the local nesting limit"}
	}
	eval := func(arg planExpression) any { return e.evaluate(arg, depth+1) }
	if expression.base != nil {
		base, selector := eval(*expression.base), eval(*expression.selector)
		if index, ok := selector.(float64); ok {
			if unknown, ok := base.(planUnknown); ok {
				unknown.nonEmpty = false
				return unknown
			}
			values, ok := base.([]any)
			if !ok || index < 0 || index != math.Trunc(index) || index >= float64(len(values)) {
				return planUnknown{
					reason:   "array index cannot be resolved from the actual plan",
					bindings: planBindings(base, selector),
				}
			}
			return values[int(index)]
		}
		key, missing := planString(selector)
		if missing != "" {
			return planUnknown{reason: missing, bindings: planBindings(base, selector)}
		}
		return planField(base, key)
	}
	if expression.name == "" {
		return expression.literal
	}
	name := strings.ToLower(expression.name)
	if name == "coalesce" && len(expression.args) > 0 {
		var firstUnknown *planUnknown
		var mask planBindingMask
		for _, arg := range expression.args {
			var value any
			if firstUnknown != nil {
				next := arg
				e.scanBindings(func() any {
					value = eval(next)
					return nil
				})
			} else {
				value = eval(arg)
			}
			if unknown, ok := value.(planUnknown); ok {
				mask |= unknown.bindings
				if firstUnknown == nil {
					first := unknown
					firstUnknown = &first
				}
				if unknown.nonEmpty {
					return planUnknown{
						reason:   firstUnknown.reason,
						bindings: mask,
						nonEmpty: firstUnknown.nonEmpty,
					}
				}
				continue
			}
			if value != nil {
				if firstUnknown != nil {
					return planUnknown{
						reason:   firstUnknown.reason,
						bindings: mask | planBindings(value),
					}
				}
				return value
			}
		}
		if firstUnknown != nil {
			return planUnknown{reason: firstUnknown.reason, bindings: mask}
		}
		return nil
	}
	if name == "tryget" && len(expression.args) >= 2 {
		value := eval(expression.args[0])
		var selectorMask planBindingMask
		for i, arg := range expression.args[1:] {
			if value == nil {
				return nil
			}
			if unknown, ok := value.(planUnknown); ok {
				for _, remaining := range expression.args[i+1:] {
					next := remaining
					selectorMask |= e.scanBindings(func() any { return eval(next) })
				}
				unknown.bindings |= selectorMask
				unknown.nonEmpty = false
				return unknown
			}
			selector := eval(arg)
			selectorMask |= planBindings(selector)
			switch current := value.(type) {
			case map[string]any:
				key, missing := planString(selector)
				if missing != "" {
					return planUnknown{reason: missing, bindings: planBindings(value) | selectorMask}
				}
				value = nil
				for name, item := range current {
					if strings.EqualFold(name, key) {
						value = item
						break
					}
				}
			case []any:
				index, ok := selector.(float64)
				if !ok || index != math.Trunc(index) {
					return planUnknown{
						reason:   "tryGet array index is not a resolved integer",
						bindings: planBindings(value) | selectorMask,
					}
				}
				value = nil
				if index >= 0 && index < float64(len(current)) {
					value = current[int(index)]
				}
			default:
				return planUnknown{
					reason:   "tryGet input is not a locally resolved object or array",
					bindings: planBindings(value) | selectorMask,
				}
			}
		}
		return value
	}
	if name == "if" && len(expression.args) == 3 {
		condVal := eval(expression.args[0])
		condition, missing := planBool(condVal)
		if missing != "" {
			mask := planBindings(condVal) | e.scanBindings(
				func() any { return eval(expression.args[1]) },
				func() any { return eval(expression.args[2]) },
			)
			return planUnknown{reason: "if condition cannot be resolved: " + missing, bindings: mask}
		}
		if condition {
			return eval(expression.args[1])
		}
		return eval(expression.args[2])
	}
	if (name == "and" || name == "or") && len(expression.args) >= 2 {
		var unresolved string
		var mask planBindingMask
		for _, arg := range expression.args {
			argVal := eval(arg)
			value, missing := planBool(argVal)
			if missing != "" {
				if unresolved == "" {
					unresolved = missing
				}
				mask |= planBindings(argVal)
			} else if value == (name == "or") {
				return value
			}
		}
		if unresolved != "" {
			return planUnknown{reason: unresolved, bindings: mask}
		}
		return name == "and"
	}
	args := make([]any, len(expression.args))
	for i, arg := range expression.args {
		args[i] = eval(arg)
	}
	argBindings := planBindings(args...)
	if name == "createarray" {
		return args
	}
	if name == "createobject" && len(args)%2 == 0 {
		result := map[string]any{}
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				return planUnknown{reason: "createObject key is not a resolved string", bindings: argBindings}
			}
			result[key] = args[i+1]
		}
		return result
	}
	if name == "resourceid" || name == "subscriptionresourceid" || name == "extensionresourceid" {
		resolved := e.resourceID(name, args)
		switch current := resolved.(type) {
		case planUnknown:
			current.bindings |= argBindings
			return current
		case planPartialID:
			current.bindings |= argBindings
			return current
		default:
			return resolved
		}
	}
	if name == "split" && len(args) == 2 && args[1] == "/" {
		if partial, ok := args[0].(planPartialID); ok && partial.components != nil {
			return slices.Clone(partial.components)
		}
	}
	if name == "empty" && len(args) == 1 {
		if unknown, ok := args[0].(planUnknown); ok && unknown.nonEmpty && unknown.bindings == 0 {
			return false
		}
	}
	for _, arg := range args {
		if unknown, ok := arg.(planUnknown); ok {
			unknown.bindings = argBindings
			unknown.nonEmpty = false
			return unknown
		}
		if partial, ok := arg.(planPartialID); ok {
			return planUnknown{reason: partial.missing, bindings: argBindings}
		}
	}
	if len(args) == 0 {
		switch name {
		case "true":
			return true
		case "false":
			return false
		case "null":
			return nil
		case "subscription":
			if e.scope.missing != "" {
				return planUnknown{reason: e.scope.missing}
			}
			return map[string]any{"id": e.scope.subscriptionARMID, "subscriptionId": e.scope.subscriptionID}
		case "resourcegroup":
			if e.scope.missing != "" || e.scope.groupARMID == "" {
				return planUnknown{reason: "resource-group context is not resolved in " + e.path}
			}
			var location any = e.scope.location
			if e.scope.location == "" {
				location = planUnknown{reason: "actual resource-group location has not been resolved"}
			}
			return map[string]any{"id": e.scope.groupARMID, "name": e.scope.group, "location": location}
		case "deployment":
			return map[string]any{"name": e.deployment}
		case "copyindex":
			return float64(e.copyIndex)
		}
	}
	if len(args) == 1 {
		switch name {
		case "parameters":
			if key, ok := args[0].(string); ok {
				return e.parameter(key)
			}
		case "variables":
			if key, ok := args[0].(string); ok {
				return e.variable(key)
			}
		case "not":
			if value, ok := args[0].(bool); ok {
				return !value
			}
		case "empty", "length":
			length := -1
			switch value := args[0].(type) {
			case string:
				if name == "length" && strings.ContainsFunc(value, func(r rune) bool { return r > 127 }) {
					return planUnknown{
						reason:   "non-ASCII string length is not evaluated by the local plan adapter",
						bindings: argBindings,
					}
				}
				length = len(value)
			case []any:
				length = len(value)
			case map[string]any:
				length = len(value)
			case nil:
				if name == "empty" {
					return true
				}
			}
			if length >= 0 {
				if name == "empty" {
					return length == 0
				}
				return float64(length)
			}
		case "first", "last":
			if values, ok := args[0].([]any); ok && len(values) > 0 {
				if name == "first" {
					return values[0]
				}
				return values[len(values)-1]
			}
		case "tolower", "toupper", "trim":
			if value, ok := args[0].(string); ok {
				switch name {
				case "tolower":
					return strings.ToLower(value)
				case "toupper":
					return strings.ToUpper(value)
				default:
					return strings.TrimSpace(value)
				}
			}
		case "json":
			if value, ok := args[0].(string); ok {
				var decoded any
				if err := json.Unmarshal([]byte(value), &decoded); err == nil {
					return decoded
				}
			}
		case "shallowmerge":
			if values, ok := args[0].([]any); ok {
				result := map[string]any{}
				for _, value := range values {
					object, ok := value.(map[string]any)
					if !ok {
						return planUnknown{reason: "shallowMerge input is not a resolved object", bindings: argBindings}
					}
					maps.Copy(result, object)
				}
				return result
			}
		}
	}
	if name == "reference" || name == "resourceinfo" {
		full := name == "resourceinfo"
		if name == "reference" && len(args) == 3 {
			mode, ok := args[2].(string)
			if !ok || !strings.EqualFold(mode, "Full") {
				return planUnknown{
					reason:   "reference mode is not supported by the local plan adapter",
					bindings: argBindings,
				}
			}
			full = true
		}
		if len(args) >= 1 {
			if symbol, ok := args[0].(string); ok {
				if resource := e.resources[symbol]; resource != nil {
					return resource.reference(full)
				}
				if parsed, err := arm.ParseResourceID(symbol); err == nil && parsed.SubscriptionID != "" {
					for _, resource := range e.resources {
						if !strings.EqualFold(resource.kind, parsed.ResourceType.String()) {
							continue
						}
						if id, missing := resource.id(); missing == "" && strings.EqualFold(id, symbol) {
							return resource.reference(full)
						}
					}
				}
				return planUnknown{
					reason:   "runtime reference " + symbol + " is not a local resource/module",
					bindings: argBindings,
				}
			}
		}
	}
	if len(args) == 2 {
		switch name {
		case "equals":
			if !planScalar(args[0]) || !planScalar(args[1]) {
				return planUnknown{
					reason:   "structured or runtime-symbolic equality is not locally resolved",
					bindings: argBindings,
				}
			}
			a, errA := json.Marshal(args[0])
			b, errB := json.Marshal(args[1])
			if errA == nil && errB == nil {
				return string(a) == string(b)
			}
		case "contains":
			switch value := args[0].(type) {
			case []any:
				if !planScalar(args[1]) {
					return planUnknown{
						reason:   "contains requires a locally resolved scalar search value",
						bindings: argBindings,
					}
				}
				var unresolved bool
				expected, _ := json.Marshal(args[1])
				for _, item := range value {
					if !planScalar(item) {
						unresolved = true
						continue
					}
					actual, _ := json.Marshal(item)
					if string(actual) == string(expected) {
						return true
					}
				}
				if unresolved {
					return planUnknown{
						reason:   "contains has unresolved or structured array entries",
						bindings: argBindings,
					}
				}
				return false
			case string:
				if part, ok := args[1].(string); ok {
					return strings.Contains(value, part)
				}
			}
		case "split":
			if text, ok := args[0].(string); ok {
				if separator, ok := args[1].(string); ok {
					parts := strings.Split(text, separator)
					result := make([]any, len(parts))
					for i, part := range parts {
						result[i] = part
					}
					return result
				}
			}
		}
	}
	stringsOnly := make([]string, len(args))
	for i, arg := range args {
		value, ok := arg.(string)
		if !ok {
			return planUnknown{
				reason:   "ARM function " + expression.name + " has unsupported plan argument types",
				bindings: argBindings,
			}
		}
		stringsOnly[i] = value
	}
	switch name {
	case "concat":
		return strings.Join(stringsOnly, "")
	case "format":
		if len(stringsOnly) > 0 {
			result := stringsOnly[0]
			for i, value := range stringsOnly[1:] {
				result = strings.ReplaceAll(result, "{"+strconv.Itoa(i)+"}", value)
			}
			if strings.ContainsAny(result, "{}") {
				return planUnknown{reason: "format uses unsupported placeholders", bindings: argBindings}
			}
			return result
		}
	case "replace":
		if len(stringsOnly) == 3 {
			return strings.ReplaceAll(stringsOnly[0], stringsOnly[1], stringsOnly[2])
		}
	case "uniquestring", "guid":
		if len(stringsOnly) > 0 {
			return e.function(name, stringsOnly)
		}
	}
	return planUnknown{
		reason:   "ARM function " + expression.name + " is not supported by the local plan adapter",
		bindings: argBindings,
	}
}

func (e *planEnvironment) resourceID(function string, args []any) any {
	typeIndex := -1
	for i, arg := range args {
		value, missing := planString(arg)
		if missing == "" && !strings.HasPrefix(value, "/") && strings.Contains(value, ".") &&
			strings.Contains(value, "/") {
			typeIndex = i
			break
		}
	}
	if typeIndex < 0 || typeIndex == len(args)-1 {
		return planUnknown{reason: "resourceId has no resolved type/name"}
	}
	scope := e.scope.groupARMID
	prefix := make([]string, typeIndex)
	for i := range typeIndex {
		value, missing := planString(args[i])
		if missing != "" {
			known := ""
			if function == "resourceid" && i == 1 {
				known = "/subscriptions/" + prefix[0]
			}
			kind, _ := planString(args[typeIndex])
			return planPartialID{kind: kind, scope: known, missing: "resourceId scope: " + missing}
		}
		prefix[i] = value
	}
	switch function {
	case "extensionresourceid":
		if typeIndex != 1 {
			return planUnknown{reason: "extensionResourceId scope cannot be resolved"}
		}
		scope = prefix[0]
	case "subscriptionresourceid":
		scope = e.scope.subscriptionARMID
		if typeIndex == 1 {
			scope = "/subscriptions/" + prefix[0]
		} else if typeIndex != 0 {
			return planUnknown{reason: "subscriptionResourceId scope cannot be resolved"}
		}
	case "resourceid":
		if typeIndex == 1 {
			scope = e.scope.subscriptionARMID + "/resourceGroups/" + prefix[0]
		} else if typeIndex == 2 {
			scope = "/subscriptions/" + prefix[0] + "/resourceGroups/" + prefix[1]
		} else if typeIndex != 0 {
			return planUnknown{reason: "resourceId scope cannot be resolved"}
		}
	}
	if scope == "" || e.scope.missing != "" && typeIndex == 0 {
		return planUnknown{reason: "resourceId deployment scope cannot be resolved"}
	}
	kind, _ := planString(args[typeIndex])
	var parts []string
	var components []any
	for part := range strings.SplitSeq(scope, "/") {
		components = append(components, part)
	}
	types := strings.Split(kind, "/")
	components = append(components, "providers", types[0])
	var nameMissing string
	for _, argument := range args[typeIndex+1:] {
		name, missing := planString(argument)
		if missing != "" {
			nameMissing = "resourceId resource name: " + missing
			parts = append(parts, "")
		} else {
			parts = append(parts, name)
		}
	}
	if nameMissing != "" {
		if len(types)-1 == len(parts) {
			for i, argument := range args[typeIndex+1:] {
				components = append(components, types[i+1], argument)
			}
		} else {
			components = nil
		}
		return planPartialID{kind: kind, scope: scope, missing: nameMissing, components: components}
	}
	names := strings.Split(strings.Join(parts, "/"), "/")
	if len(types)-1 != len(names) {
		return planUnknown{reason: "resourceId type/name hierarchy cannot be resolved"}
	}
	var result strings.Builder
	result.WriteString(scope + "/providers/" + types[0])
	for i, name := range names {
		result.WriteString("/" + types[i+1] + "/" + name)
	}
	return result.String()
}
