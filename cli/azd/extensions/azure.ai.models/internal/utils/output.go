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
)

// OutputFormat represents the output format type
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
)

// PrintObject prints a struct or slice in the specified format
func PrintObject(obj interface{}, format OutputFormat) error {
	switch format {
	case FormatJSON:
		return printJSON(obj)
	case FormatTable:
		return printTable(obj)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func printJSON(obj interface{}) error {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

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

type columnInfo struct {
	header string
	index  int
}

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

func printSliceAsTable(v reflect.Value) error {
	if v.Len() == 0 {
		fmt.Println("No items to display")
		return nil
	}

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

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	headers := make([]string, len(cols))
	for i, col := range cols {
		headers[i] = col.header
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	separators := make([]string, len(cols))
	for i, col := range cols {
		separators[i] = strings.Repeat("-", len(col.header))
	}
	fmt.Fprintln(w, strings.Join(separators, "\t"))

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

func formatFieldValue(v reflect.Value) string {
	if !v.IsValid() {
		return "-"
	}

	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "-"
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		if v.String() == "" {
			return "-"
		}
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
