package runner

import (
	"errors"
	"fmt"

	"github.com/akyrey/projector/internal/config"
)

// ErrCyclicDependency is returned when depends_on forms a cycle.
var ErrCyclicDependency = errors.New("cyclic dependency detected")

// ErrUnknownDependency is returned when a depends_on entry names a command that
// does not exist in the merged config.
var ErrUnknownDependency = errors.New("unknown dependency")

// ResolveDependencyOrder performs a topological sort (Kahn's algorithm) on the
// commands reachable from roots, returning an ordered slice of command names
// where every dependency appears before the command that needs it.
//
// roots is the set of command names explicitly requested by the user.
// commands is the full merged command map for the current context.
//
// The returned slice always ends with the root commands themselves (in the order
// they were provided), so callers can distinguish "dependencies to run first"
// from "the commands requested".
//
// Returns ErrCyclicDependency if any cycle is detected, or ErrUnknownDependency
// if a depends_on entry references a command that is not defined.
func ResolveDependencyOrder(roots []string, commands map[string]config.Command) ([]string, error) {
	// 1. Collect the full transitive closure of commands we need to consider.
	needed, err := transitiveClosure(roots, commands)
	if err != nil {
		return nil, err
	}

	// 2. Build in-degree map and adjacency list restricted to needed nodes.
	//    Edge direction: dependency → dependent  (dep must run before dependent)
	inDegree := make(map[string]int, len(needed))
	// dependents[dep] = list of commands that depend on dep
	dependents := make(map[string][]string, len(needed))

	for name := range needed {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
		for _, dep := range commands[name].DependsOn {
			dependents[dep] = append(dependents[dep], name)
			inDegree[name]++
		}
	}

	// 3. Kahn's BFS: start with nodes that have no dependencies.
	queue := make([]string, 0, len(needed))
	for name := range needed {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	// Stable sort the initial queue so output is deterministic.
	sortStrings(queue)

	order := make([]string, 0, len(needed))
	for len(queue) > 0 {
		// Dequeue front.
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		// Reduce in-degree of each dependent.
		deps := dependents[node]
		sortStrings(deps) // deterministic ordering
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	// 4. If not all nodes were processed, a cycle exists.
	if len(order) != len(needed) {
		cycle := findCycle(roots, commands)
		return nil, fmt.Errorf("%w: %s", ErrCyclicDependency, cycle)
	}

	return order, nil
}

// transitiveClosure collects the names of all commands transitively reachable
// from roots via depends_on edges.
func transitiveClosure(roots []string, commands map[string]config.Command) (map[string]struct{}, error) {
	visited := make(map[string]struct{})
	stack := make([]string, len(roots))
	copy(stack, roots)

	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if _, seen := visited[name]; seen {
			continue
		}
		visited[name] = struct{}{}

		cmd, ok := commands[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownDependency, name)
		}

		for _, dep := range cmd.DependsOn {
			if _, seen := visited[dep]; !seen {
				stack = append(stack, dep)
			}
		}
	}

	return visited, nil
}

// findCycle walks the graph looking for a cycle and returns a human-readable
// description like "a -> b -> c -> a".  It is called only when a cycle is known
// to exist, so it always finds one.
func findCycle(roots []string, commands map[string]config.Command) string {
	// DFS with a color map: 0=white, 1=grey (in stack), 2=black (done).
	color := make(map[string]int)
	path := make([]string, 0)

	var dfs func(name string) []string
	dfs = func(name string) []string {
		color[name] = 1
		path = append(path, name)

		cmd, ok := commands[name]
		if !ok {
			color[name] = 2
			path = path[:len(path)-1]
			return nil
		}

		for _, dep := range cmd.DependsOn {
			if color[dep] == 1 {
				// Found the back edge; extract the cycle.
				for i, n := range path {
					if n == dep {
						cycle := append(path[i:], dep) //nolint:gocritic // intentional copy
						return cycle
					}
				}
			}
			if color[dep] == 0 {
				if c := dfs(dep); c != nil {
					return c
				}
			}
		}

		color[name] = 2
		path = path[:len(path)-1]
		return nil
	}

	for _, root := range roots {
		if color[root] == 0 {
			if cycle := dfs(root); cycle != nil {
				result := cycle[0]
				for _, n := range cycle[1:] {
					result += " -> " + n
				}
				return result
			}
		}
	}

	return "(unknown cycle)"
}

// sortStrings sorts a string slice in-place (insertion sort — short slices only).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
