// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"

	"azure.ai.finetune/pkg/models"
)

// OutputFormat represents the output format type
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
	FormatYAML  OutputFormat = "yaml"

	// dateTimeFormat is the standard format for displaying timestamps
	dateTimeFormat = "2006-01-02 15:04"
)

var (
	timeDurationType   = reflect.TypeOf(time.Duration(0))
	modelsDurationType = reflect.TypeOf(models.Duration(0))
)

// PrintObject prints a struct or slice in the specified format
func PrintObject(obj interface{}, format OutputFormat) error {
	switch format {
	case FormatJSON:
		return printJSON(obj)
	case FormatYAML:
		return printYAML(obj)
	case FormatTable:
		return printTable(obj)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

// printJSON uses encoding/json which respects `json` tags
func printJSON(obj interface{}) error {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// printYAML uses gopkg.in/yaml.v3 which respects `yaml` tags
func printYAML(obj interface{}) error {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	fmt.Print(string(data))
	return nil
}

// printTable uses text/tabwriter and reads `table` tags
func printTable(obj interface{}) error {
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() == reflect.Slice {
		return printSliceAsTable(v)
	}

	if v.Kind() == reflect.Struct {
		return printStructAsKeyValue(v)
	}

	return fmt.Errorf("table format requires a struct or slice, got %s", v.Kind())
}

// columnInfo holds table column metadata
type columnInfo struct {
	header string
	index  int
}

// getTableColumns extracts fields with `table` tags
func getTableColumns(t reflect.Type) []columnInfo {
	var cols []columnInfo
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("table")
		if tag != "" && tag != "-" {
			cols = append(cols, columnInfo{header: tag, index: i})
		}
	}
	return cols
}

// printSliceAsTable prints a slice of structs as a table with headers and rows
func printSliceAsTable(v reflect.Value) error {
	if v.Len() == 0 {
		fmt.Println("No items to display")
		return nil
	}

	// Get element type
	firstElem := v.Index(0)
	if firstElem.Kind() == reflect.Ptr {
		firstElem = firstElem.Elem()
	}

	if firstElem.Kind() != reflect.Struct {
		return fmt.Errorf("slice elements must be structs, got %s", firstElem.Kind())
	}

	cols := getTableColumns(firstElem.Type())
	if len(cols) == 0 {
		return fmt.Errorf("no fields with 'table' struct tags found in type %s", firstElem.Type().Name())
	}

	// Create tabwriter
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print header row
	headers := make([]string, len(cols))
	for i, col := range cols {
		headers[i] = col.header
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	// Print separator row
	separators := make([]string, len(cols))
	for i, col := range cols {
		separators[i] = strings.Repeat("-", len(col.header))
	}
	fmt.Fprintln(w, strings.Join(separators, "\t"))

	// Print data rows
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}

		values := make([]string, len(cols))
		for j, col := range cols {
			values[j] = formatFieldValue(elem.Field(col.index))
		}
		fmt.Fprintln(w, strings.Join(values, "\t"))
	}

	return w.Flush()
}

// printStructAsKeyValue prints a single struct as key-value pairs
func printStructAsKeyValue(v reflect.Value) error {
	t := v.Type()
	cols := getTableColumns(t)

	if len(cols) == 0 {
		return fmt.Errorf("no fields with table tags found")
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for _, col := range cols {
		value := formatFieldValue(v.Field(col.index))
		fmt.Fprintf(w, "%s:\t%s\n", col.header, value)
	}

	return w.Flush()
}

// PrintObjectWithIndent prints a struct or slice in the specified format with indentation
func PrintObjectWithIndent(obj interface{}, format OutputFormat, indent string) error {
	if format != FormatTable {
		return PrintObject(obj, format)
	}

	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", v.Kind())
	}

	t := v.Type()
	cols := getTableColumns(t)

	if len(cols) == 0 {
		return fmt.Errorf("no fields with table tags found")
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for _, col := range cols {
		value := formatFieldValue(v.Field(col.index))
		fmt.Fprintf(w, "%s%s:\t%s\n", indent, col.header, value)
	}

	return w.Flush()
}

// formatFieldValue converts a reflect.Value to a string representation
func formatFieldValue(v reflect.Value) string {
	if !v.IsValid() {
		return "-"
	}

	// Handle pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "-"
		}
		v = v.Elem()
	}

	// Handle time.Time
	if t, ok := v.Interface().(time.Time); ok {
		return t.Format(dateTimeFormat)
	}

	// Handle time.Duration
	if v.Type() == timeDurationType || v.Type() == modelsDurationType {
		d := time.Duration(v.Int())
		if d == 0 {
			return "-"
		}
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %02dm", h, m)
	}

	switch v.Kind() {
	case reflect.String:
		if v.String() == "" {
			return "-"
		}
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%.4f", v.Float())
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
