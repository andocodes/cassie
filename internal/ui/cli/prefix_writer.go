package cli

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

type sharedWriter struct {
	writer io.Writer
	mu     sync.Mutex
}

type prefixWriter struct {
	shared *sharedWriter
	prefix string
	buffer bytes.Buffer
}

func newPrefixWriter(shared *sharedWriter, app string, color lipgloss.Color) *prefixWriter {
	prefix := lipgloss.NewStyle().Bold(true).Foreground(color).Render("[" + app + "]")
	return &prefixWriter{shared: shared, prefix: prefix}
}

func (w *prefixWriter) Write(content []byte) (int, error) {
	w.buffer.Write(content)
	for {
		line, err := w.buffer.ReadString('\n')
		if err != nil {
			w.buffer.WriteString(line)
			break
		}
		if writeErr := w.line(line); writeErr != nil {
			return 0, writeErr
		}
	}
	return len(content), nil
}

func (w *prefixWriter) Flush() error {
	if w.buffer.Len() == 0 {
		return nil
	}
	line := w.buffer.String()
	w.buffer.Reset()
	return w.line(line + "\n")
}

func (w *prefixWriter) line(line string) error {
	w.shared.mu.Lock()
	defer w.shared.mu.Unlock()
	_, err := fmt.Fprintf(w.shared.writer, "%s %s", w.prefix, line)
	return err
}
