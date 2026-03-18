package cli

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/akyrey/projector/internal/config"
)

// pickCommand presents an interactive numbered list of commands from merged
// and reads a selection from in. It returns the chosen command name.
// out is where the list is printed (typically cmd.OutOrStdout()).
func pickCommand(commands map[string]config.Command, in io.Reader, out io.Writer) (string, error) {
	if len(commands) == 0 {
		return "", fmt.Errorf("no commands available to choose from")
	}

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintln(out, "Choose a command:")
	for i, name := range names {
		cmd := commands[name]
		if cmd.Description != "" {
			fmt.Fprintf(out, "  %2d) %-20s %s\n", i+1, name, cmd.Description)
		} else {
			fmt.Fprintf(out, "  %2d) %s\n", i+1, name)
		}
	}
	fmt.Fprint(out, "Enter number or name: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read input: %w", err)
		}
		return "", fmt.Errorf("no selection made (EOF)")
	}

	input := strings.TrimSpace(scanner.Text())
	if input == "" {
		return "", fmt.Errorf("no selection made")
	}

	// Try numeric selection.
	if n, err := strconv.Atoi(input); err == nil {
		if n < 1 || n > len(names) {
			return "", fmt.Errorf("selection %d out of range (1-%d)", n, len(names))
		}
		return names[n-1], nil
	}

	// Try name match (exact).
	if _, ok := commands[input]; ok {
		return input, nil
	}

	// Try prefix match.
	var matches []string
	for _, name := range names {
		if strings.HasPrefix(name, input) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no command matching %q", input)
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches: %v", input, matches)
	}
}
