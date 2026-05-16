package util

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// PrefixWriter wraps an io.Writer and prepends "[prefix] " to every complete
// line. It is safe for concurrent use: the internal mutex serializes both the
// partial-line buffer and the final write to the underlying writer, so output
// from multiple goroutines writing to the same PrefixWriter does not interleave.
type PrefixWriter struct {
	prefix  string
	out     io.Writer
	mu      sync.Mutex
	partial []byte
}

func NewPrefixWriter(prefix string, out io.Writer) *PrefixWriter {
	return &PrefixWriter{prefix: prefix, out: out}
}

func (w *PrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.partial = append(w.partial, p...)
	for {
		idx := bytes.IndexByte(w.partial, '\n')
		if idx < 0 {
			break
		}
		line := w.partial[:idx]
		w.partial = w.partial[idx+1:]
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		fmt.Fprintf(w.out, "[%s] %s\n", w.prefix, line)
	}
	return len(p), nil
}
