package add

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"
	dmp "github.com/sergi/go-diff/diffmatchpatch"
)

// DiffBlocks returns a textual diff of new - old.
//
// It compares the values in old and new, and generates a textual diff for each value difference between old and new.
// It doesn't currently support deletions of new from old.
func DiffBlocks(old map[string]*project.ResourceConfig, new map[string]*project.ResourceConfig) (string, error) {
	diffObj := dmp.New()

	// dynamic programming: store marshaled entries for comparison
	oldMarshaled := map[string]string{}
	newMarshaled := map[string]string{}

	allDiffs := []diffBlock{}
	for key, newVal := range new {
		oldVal, ok := old[key]
		if !ok {
			contents, err := yaml.Marshal(newVal)
			if err != nil {
				return "", fmt.Errorf("marshaling new %v: %w", key, err)
			}
			allDiffs = append(allDiffs, diffBlock{
				Header: diffLine{Type: dmp.DiffInsert, Text: key + ":"},
				Lines:  lineDiffsFromStr(dmp.DiffInsert, string(contents)),
				Indent: 4,
			})
			continue
		}

		newContent, ok := newMarshaled[key]
		if !ok {
			content, err := yaml.Marshal(newVal)
			if err != nil {
				return "", fmt.Errorf("marshaling new %v: %w", key, err)
			}
			newContent = string(content)
			newMarshaled[key] = newContent
		}

		oldContent, ok := oldMarshaled[key]
		if !ok {
			content, err := yaml.Marshal(oldVal)
			if err != nil {
				return "", fmt.Errorf("marshaling old %v: %w", key, err)
			}
			oldContent = string(content)
			oldMarshaled[key] = oldContent
		}

		diffs := diffObj.DiffMain(oldContent, newContent, false)
		if diffNotEq(diffs) {
			allDiffs = append(allDiffs, diffBlock{
				Header: diffLine{Type: dmp.DiffEqual, Text: key + ":"},
				Lines:  linesDiffsFromTextDiffs(diffs),
				Indent: 4,
			})
		}
	}

	slices.SortFunc(allDiffs, func(a, b diffBlock) int {
		return strings.Compare(a.Header.Text, b.Header.Text)
	})
	var sb strings.Builder
	for _, s := range allDiffs {
		sb.WriteString(formatLine(s.Header.Type, s.Header.Text, 0))

		for _, r := range s.Lines {
			if len(r.Text) == 0 { // trim empty lines
				continue
			}

			sb.WriteString(formatLine(r.Type, r.Text, s.Indent))
		}

		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func formatLine(op dmp.Operation, text string, indent int) string {
	switch op {
	case dmp.DiffInsert:
		return color.GreenString("+  %s%s\n", strings.Repeat(" ", indent), text)
	case dmp.DiffDelete:
		return color.RedString("-  %s%s\n", strings.Repeat(" ", indent), text)
	case dmp.DiffEqual:
		return fmt.Sprintf("   %s%s\n", strings.Repeat(" ", indent), text)
	default:
		panic("unreachable")
	}
}

func diffNotEq(diffs []dmp.Diff) bool {
	for _, diff := range diffs {
		if diff.Type != dmp.DiffEqual {
			return true
		}
	}

	return false
}

func lineDiffsFromStr(op dmp.Operation, s string) []diffLine {
	var result []diffLine

	lines := strings.Split(s, "\n")
	for _, line := range lines {
		result = append(result, diffLine{Text: line, Type: op})
	}

	return result
}

// diffBlock is a block of lines of text diffs to be displayed.
// The lines are indented by Indent.
type diffBlock struct {
	Header diffLine
	Lines  []diffLine
	Indent int
}

// diffLine is a text diff on a line-by-line basis.
type diffLine struct {
	Text string
	Type dmp.Operation
}

func linesDiffsFromTextDiffs(diffs []dmp.Diff) []diffLine {
	var result []diffLine

	for _, diff := range diffs {
		lines := strings.Split(diff.Text, "\n")

		for _, line := range lines {
			result = append(result, diffLine{Text: line, Type: diff.Type})
		}
	}

	return result
}
