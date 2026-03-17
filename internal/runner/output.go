package runner

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/fatih/color"
)

// prefixColors is the ordered palette used to assign colors to project prefixes.
// Colors cycle when there are more projects than entries.
var prefixColors = []*color.Color{
	color.New(color.FgCyan, color.Bold),
	color.New(color.FgGreen, color.Bold),
	color.New(color.FgYellow, color.Bold),
	color.New(color.FgMagenta, color.Bold),
	color.New(color.FgBlue, color.Bold),
	color.New(color.FgRed, color.Bold),
	color.New(color.FgHiCyan, color.Bold),
	color.New(color.FgHiGreen, color.Bold),
}

// colorFor returns a deterministic color for the given index.
func colorFor(i int) *color.Color {
	return prefixColors[i%len(prefixColors)]
}

// prefixWriter is an io.Writer that prepends a colored label to every line.
// It is safe to use from multiple goroutines (output serialized via mu).
type prefixWriter struct {
	mu     *sync.Mutex
	out    io.Writer
	prefix string
}

// newPrefixWriter creates a prefixWriter with a label formatted as "[label] ".
// The label is rendered in the given color.
func newPrefixWriter(out io.Writer, mu *sync.Mutex, label string, c *color.Color) *prefixWriter {
	colored := c.Sprintf("[%s]", label)
	return &prefixWriter{
		mu:     mu,
		out:    out,
		prefix: colored + " ",
	}
}

// Write prepends the prefix to each line in p and writes to the underlying writer.
// Partial lines (not terminated by \n) are flushed immediately with the prefix.
func (pw *prefixWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")

	pw.mu.Lock()
	defer pw.mu.Unlock()

	for i, line := range lines {
		// The split produces an empty trailing element after a final \n.
		// Skip it to avoid printing a blank prefixed line.
		if i == len(lines)-1 && line == "" {
			continue
		}
		if _, err := fmt.Fprintf(pw.out, "%s%s\n", pw.prefix, line); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}
