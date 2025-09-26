// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/tabwriter"
	"text/template"
)

// Based on https://golang.org/pkg/text/tabwriter/
const (
	TableColumnMinWidth = 10
	TableTabSize        = 4
	TablePadSize        = 2
	TablePadCharacter   = ' '
	TableFlags          = 0
)

type TableFormatterOptions struct {
	Columns []Column
}

type Column struct {
	Heading       string
	ValueTemplate string
	Transformer   func(string) string
}

type TableFormatter struct {
}

func (f *TableFormatter) Kind() Format {
	return TableFormat
}

func (f *TableFormatter) Format(obj interface{}, writer io.Writer, opts interface{}) error {
	options, ok := opts.(TableFormatterOptions)
	if !ok {
		return errors.New("invalid formatter options, TableFormatterOptions expected")
	}

	if len(options.Columns) == 0 {
		return errors.New("no columns were defined, table format is not supported for this command")
	}

	rows, err := convertToSlice(obj)
	if err != nil {
		return err
	}

	headings := []string{}
	templates := []*template.Template{}
	transformers := []func(string) string{}

	for _, c := range options.Columns {
		headings = append(headings, c.Heading)

		t, err := template.New(c.Heading).Parse(c.ValueTemplate)
		if err != nil {
			return err
		}
		templates = append(templates, t)
		transformers = append(transformers, c.Transformer)
	}

	tabs := tabwriter.NewWriter(writer, TableColumnMinWidth, TableTabSize, TablePadSize, TablePadCharacter, TableFlags)
	_, err = tabs.Write([]byte(strings.Join(headings, "\t") + "\n"))
	if err != nil {
		return err
	}

	for _, row := range rows {
		for i, t := range templates {
			xfm := transformers[i]

			if xfm != nil {
				buf := bytes.Buffer{}
				err := t.Execute(&buf, row)
				if err != nil {
					return err
				}
				_, err = tabs.Write([]byte(xfm(buf.String())))
				if err != nil {
					return err
				}
			} else {
				err = t.Execute(tabs, row)
				if err != nil {
					return err
				}
			}

			if i < len(templates)-1 {
				_, err = tabs.Write([]byte("\t"))
				if err != nil {
					return err
				}
			}
		}

		_, err := tabs.Write([]byte("\n"))
		if err != nil {
			return err
		}
	}

	err = tabs.Flush()
	if err != nil {
		return err
	}

	return nil
}

func convertToSlice(obj interface{}) ([]interface{}, error) {
	// We use reflection here because we're building a table and thus need to handle both scalars (structs)
	// and slices/arrays of structs.
	var vv []interface{}
	v := reflect.ValueOf(obj)

	// Follow pointers at the top level
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, fmt.Errorf("value is nil")
		}

		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct, reflect.Interface:
		vv = append(vv, v.Interface())
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			item := v.Index(i)
			vv = append(vv, item.Interface())
		}
	default:
		return nil, fmt.Errorf("unsupported value kind: %v", v.Kind())
	}

	return vv, nil
}

// TabAlign transforms translates tab-separated columns in input into properly aligned text
// with the given padding for separation.
// For more information, refer to the tabwriter package.
func TabAlign(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

var _ Formatter = (*TableFormatter)(nil)
