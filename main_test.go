package main

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestExec(t *testing.T) {
	s := newServer()
	t0 := time.Now()
	ms := time.Millisecond
	ip := "203.0.113.7"
	for _, c := range []struct {
		at       time.Duration
		in, want string
	}{
		// SPEC example session
		{0, "CONFIG api 3 1", "OK"},
		{0, "TAKE api " + ip, "ALLOW 2"},
		{0, "TAKE api " + ip, "ALLOW 1"},
		{0, "TAKE api " + ip, "ALLOW 0"},
		{0, "TAKE api " + ip, "DENY 1000"},
		{0, "TAKE login " + ip, "ERR unknown limiter"},
		// lazy refill; denied requests spend nothing
		{500 * ms, "TAKE api " + ip, "DENY 500"},
		{1500 * ms, "TAKE api " + ip, "ALLOW 0"},
		// same CONFIG is a no-op; lower capacity clamps existing buckets
		{1500 * ms, "CONFIG api 3 1", "OK"},
		{1500 * ms, "TAKE api b", "ALLOW 2"},
		{1500 * ms, "CONFIG api 1 1", "OK"},
		{1500 * ms, "TAKE api b", "ALLOW 0"},
		// fractional rate
		{0, "CONFIG slow 1 0.5", "OK"},
		{0, "TAKE slow k", "ALLOW 0"},
		{0, "TAKE slow k", "DENY 2000"},
		// names
		{0, "TAKE slow " + strings.Repeat("k", 256), "ALLOW 0"},
		{0, "TAKE slow " + strings.Repeat("k", 257), "ERR bad arguments"},
		{0, "TAKE slow a\tb", "ERR bad arguments"},
		// errors
		{0, "PING", "PONG"},
		{0, "PING x", "ERR bad arguments"},
		{0, "", "ERR unknown command"},
		{0, "take api x", "ERR unknown command"},
		{0, "TAKE api", "ERR bad arguments"},
		{0, "TAKE api  x", "ERR bad arguments"},
		{0, "CONFIG api 0 1", "ERR bad arguments"},
		{0, "CONFIG api 1.5 1", "ERR bad arguments"},
		{0, "CONFIG api 1 0", "ERR bad arguments"},
		{0, "CONFIG api 1 -1", "ERR bad arguments"},
		{0, "CONFIG api 1 abc", "ERR bad arguments"},
		{0, "CONFIG api 1 inf", "ERR bad arguments"},
		{0, "CONFIG api 1 NaN", "ERR bad arguments"},
		{0, "CONFIG api 1 1e-310", "ERR bad arguments"},
	} {
		if got := s.exec(c.in, t0.Add(c.at)); got != c.want {
			t.Errorf("%q at %v: got %q, want %q", c.in, c.at, got, c.want)
		}
	}
}

func TestSweep(t *testing.T) {
	s := newServer()
	t0 := time.Now()
	s.exec("CONFIG api 2 1", t0)
	s.exec("TAKE api old", t0)
	s.exec("TAKE api new", t0.Add(time.Second))
	s.sweep(t0.Add(2 * time.Second)) // old idle 2s = capacity/rate, new only 1s
	b := s.limiters["api"].buckets
	if b["old"] != nil || b["new"] == nil {
		t.Fatalf("after sweep: old=%v new=%v", b["old"], b["new"])
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serve(ln, time.Minute)
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	expect := func(want string) {
		t.Helper()
		if got, err := r.ReadString('\n'); got != want+"\n" {
			t.Fatalf("got %q (err %v), want %q", got, err, want)
		}
	}

	// pipelined, mixed line endings
	io.WriteString(c, "PING\r\nCONFIG api 3 1\nTAKE api k\r\n")
	expect("PONG")
	expect("OK")
	expect("ALLOW 2")

	// exactly 1 KB is fine, with either line ending
	io.WriteString(c, strings.Repeat("A", maxLine)+"\n")
	expect("ERR unknown command")
	io.WriteString(c, strings.Repeat("A", maxLine)+"\r\n")
	expect("ERR unknown command")

	io.WriteString(c, strings.Repeat("A", maxLine+100)+"\n")
	expect("ERR line too long")
	if _, err := r.ReadString('\n'); err != io.EOF {
		t.Fatalf("want EOF after line too long, got %v", err)
	}
}
