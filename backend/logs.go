package main

import (
	"bytes"
	"strings"
)

const maxLogLineBytes = 8 << 10

type lineWriter struct {
	log       func(string)
	prefix    string
	pending   []byte
	truncated bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		line, rest, newline := bytes.Cut(p, []byte("\n"))
		available := maxLogLineBytes - len(w.pending)
		if len(line) > available {
			line = line[:available]
			w.truncated = true
		}
		w.pending = append(w.pending, line...)
		if newline {
			w.emit()
		}
		p = rest
	}
	return size, nil
}

func (w *lineWriter) flush() {
	if len(w.pending) > 0 {
		w.emit()
	}
}

func (w *lineWriter) emit() {
	line := strings.TrimSpace(string(w.pending))
	if w.truncated {
		line += "…"
	}
	w.log(w.prefix + line)
	w.pending = w.pending[:0]
	w.truncated = false
}
