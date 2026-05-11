// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exegraph

import (
	"cmp"
	"fmt"
	"slices"
)

// Graph holds a set of named steps with dependency edges. Steps are added via
// [AddStep] and the graph is validated via [Validate] before execution.
// Graph is not safe for concurrent use during construction; build it in a single
// goroutine, then pass it to [Run].
type Graph struct {
	steps map[string]*Step
	order []string // insertion order for deterministic iteration

	// priority caches the transitive dependent count for each step, computed
	// lazily by [Priority]. Steps with more transitive dependents should be
	// scheduled first (critical-path heuristic).
	priority map[string]int
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		steps: make(map[string]*Step),
	}
}

// AddStep registers a step. Returns an error if the step is nil, the name is
// empty, the Action is nil, or a step with the same name already exists.
func (g *Graph) AddStep(s *Step) error {
	if s == nil {
		return fmt.Errorf("step must not be nil")
	}
	if s.Name == "" {
		return fmt.Errorf("step name must not be empty")
	}
	if s.Action == nil {
		return fmt.Errorf("step %q has nil Action", s.Name)
	}
	if _, exists := g.steps[s.Name]; exists {
		return fmt.Errorf("duplicate step name %q", s.Name)
	}
	g.steps[s.Name] = s
	g.order = append(g.order, s.Name)
	g.priority = nil // invalidate cached priority on mutation
	return nil
}

// Steps returns all registered steps in insertion order. The returned slice is
// a new allocation, but the Step pointers are shared with the graph's internals.
// Callers must not modify the returned Step values.
func (g *Graph) Steps() []*Step {
	result := make([]*Step, len(g.order))
	for i, name := range g.order {
		result[i] = g.steps[name]
	}
	return result
}

// Len returns the number of steps in the graph.
func (g *Graph) Len() int {
	return len(g.steps)
}

// stepsByTag returns all steps that have the given tag, in insertion order.
func (g *Graph) stepsByTag(tag string) []*Step {
	var result []*Step
	for _, name := range g.order {
		s := g.steps[name]
		if slices.Contains(s.Tags, tag) {
			result = append(result, s)
		}
	}
	return result
}

// Validate checks the graph for missing dependency references and cycles.
// It returns a descriptive error if any problems are found.
func (g *Graph) Validate() error {
	// Check for missing dependencies (iterate in insertion order for deterministic errors).
	for _, name := range g.order {
		s := g.steps[name]
		for _, dep := range s.DependsOn {
			if _, ok := g.steps[dep]; !ok {
				return fmt.Errorf(
					"step %q depends on %q, which does not exist in the graph",
					s.Name, dep,
				)
			}
		}
	}

	// Check for cycles using DFS with three-color marking.
	// white = unvisited, gray = in current path, black = fully explored.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.steps))

	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray
		s := g.steps[name]
		for _, dep := range s.DependsOn {
			switch color[dep] {
			case gray:
				return fmt.Errorf("cycle detected: %s → %s", name, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[name] = black
		return nil
	}

	for _, name := range g.order {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}

	return nil
}

// Priority returns the transitive dependent count for the given step. Steps
// with a higher count sit on wider critical paths and should be started first.
// The result is cached and computed once across all steps on first call.
func (g *Graph) Priority(name string) int {
	if g.priority == nil {
		g.computePriority()
	}
	return g.priority[name]
}

// computePriority fills g.priority with the transitive dependent count for
// each step using DFS from each node through the reverse adjacency list.
// For a step S, the count is |{all steps that transitively depend on S}|.
func (g *Graph) computePriority() {
	n := len(g.steps)
	g.priority = make(map[string]int, n)

	// Build reverse adjacency: step → direct dependents.
	dependents := make(map[string][]string, n)
	for _, s := range g.steps {
		for _, dep := range s.DependsOn {
			dependents[dep] = append(dependents[dep], s.Name)
		}
	}

	// For each step, walk the transitive dependents subgraph via DFS.
	// With typical graphs (10-30 steps) this is fast. For graphs with 1000+
	// steps, switch to a topological-order DP (future optimization).
	for _, name := range g.order {
		visited := make(map[string]bool)
		stack := dependents[name]
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[cur] {
				continue
			}
			visited[cur] = true
			stack = append(stack, dependents[cur]...)
		}
		g.priority[name] = len(visited)
	}
}

// priorityOrder returns step names sorted by transitive dependent count
// descending, with insertion order as tiebreaker. Steps earlier in this
// list sit on wider critical paths and should be scheduled first.
func (g *Graph) priorityOrder() []string {
	if g.priority == nil {
		g.computePriority()
	}

	ordered := make([]string, len(g.order))
	copy(ordered, g.order)

	// Build insertion-order index for stable tiebreaking.
	insertIdx := make(map[string]int, len(g.order))
	for i, name := range g.order {
		insertIdx[name] = i
	}

	slices.SortStableFunc(ordered, func(a, b string) int {
		if c := cmp.Compare(g.priority[b], g.priority[a]); c != 0 {
			return c // higher dependent count first
		}
		return cmp.Compare(insertIdx[a], insertIdx[b])
	})

	return ordered
}
