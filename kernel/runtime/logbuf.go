package runtime

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// LogBuffer is an io.Writer that keeps the last N lines (the kernel's log
// as shown in the control plane) and forwards everything to another writer.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
	next  io.Writer
	part  bytes.Buffer
}

func NewLogBuffer(max int, next io.Writer) *LogBuffer {
	return &LogBuffer{max: max, next: next}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.part.Write(p)
	for {
		line, err := b.part.ReadString('\n')
		if err != nil {
			b.part.Reset()
			b.part.WriteString(line)
			break
		}
		b.lines = append(b.lines, strings.TrimRight(line, "\n"))
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
	}
	b.mu.Unlock()
	if b.next != nil {
		return b.next.Write(p)
	}
	return len(p), nil
}

func (b *LogBuffer) Tail(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.lines) {
		n = len(b.lines)
	}
	return append([]string(nil), b.lines[len(b.lines)-n:]...)
}
