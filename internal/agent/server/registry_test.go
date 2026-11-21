package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineWriterCapsLines(t *testing.T) {
	var out strings.Builder
	lw := &lineWriter{enc: json.NewEncoder(&out), flush: func() error { return nil }}
	lw.Write([]byte(strings.Repeat("a", 3*maxLine+5))) // no newline, ever
	lw.Write([]byte("b\n"))
	if len(lw.buf) != 0 {
		t.Errorf("%d bytes left buffered", len(lw.buf))
	}
	if n := strings.Count(out.String(), "\n"); n != 4 {
		t.Errorf("%d lines, want 3 cut ones and the rest", n)
	}
}
