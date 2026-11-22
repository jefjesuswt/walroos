package apps

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// halfCloser closes like the relay does when its stdin ends: only the write side.
type halfCloser struct{ *net.TCPConn }

func (h halfCloser) Close() error { return h.CloseWrite() }

// TestAskKeepsWriting: like Bun, the server drops a request whose client half-closed before
// it answered; ask only half-closes after reading the answer.
func TestAskKeepsWriting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(c)
			for line, _ := br.ReadString('\n'); line != "\r\n" && line != ""; line, _ = br.ReadString('\n') {
			}
			c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			if _, err := br.ReadByte(); err != nil && !strings.Contains(err.Error(), "timeout") {
				c.Close() // the client half-closed: give up, like Bun
				continue
			}
			c.Write([]byte("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nok"))
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if out := string(ask(halfCloser{c.(*net.TCPConn)}, c, "/health")); !strings.HasPrefix(out, "HTTP/1.1 200") {
		t.Fatalf("answer = %q", out)
	}
}
