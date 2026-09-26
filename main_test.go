package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
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
		{0, "CONFIG api 3 1", "OK"},
		{0, "TAKE api " + ip, "ALLOW 2"},
		{0, "TAKE api " + ip, "ALLOW 1"},
		{0, "TAKE api " + ip, "ALLOW 0"},
		{0, "TAKE api " + ip, "DENY 1000"},
		{0, "TAKE login " + ip, "ERR unknown limiter"},
		{500 * ms, "TAKE api " + ip, "DENY 500"},
		{1500 * ms, "TAKE api " + ip, "ALLOW 0"},
		{1500 * ms, "CONFIG api 3 1", "OK"},
		{1500 * ms, "TAKE api b", "ALLOW 2"},
		{1500 * ms, "CONFIG api 1 1", "OK"},
		{1500 * ms, "TAKE api b", "ALLOW 0"},
		{0, "CONFIG race 3 1", "OK"},
		{time.Second, "TAKE race k", "ALLOW 2"},
		{0, "TAKE race k", "ALLOW 1"},
		{0, "CONFIG slow 1 2", "OK"},
		{0, "TAKE slow k", "ALLOW 0"},
		{0, "TAKE slow k", "DENY 500"},
		{0, "TAKE slow " + strings.Repeat("k", 256), "ALLOW 0"},
		{0, "TAKE slow " + strings.Repeat("k", 257), "ERR bad arguments"},
		{0, "TAKE slow a\tb", "ERR bad arguments"},
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
		{0, "CONFIG api 1e3 1", "ERR bad arguments"},
		{0, "CONFIG api 1 1e3", "ERR bad arguments"},
		{0, "CONFIG api +5 1", "ERR bad arguments"},
		{0, "CONFIG api 5 +1", "ERR bad arguments"},
		{0, "CONFIG api 5 .5", "ERR bad arguments"},
		{0, "CONFIG api 5 0.5", "ERR bad arguments"},
		{0, "CONFIG api 5 5.", "ERR bad arguments"},
		{0, "CONFIG api 5 5.0", "ERR bad arguments"},
		{0, "CONFIG api 5 0x10", "ERR bad arguments"},
		{0, "CONFIG api 1_000 1", "ERR bad arguments"},
		{0, "CONFIG api 1000001 1", "ERR bad arguments"},
		{0, "CONFIG api 5 1000001", "ERR bad arguments"},
		{0, "CONFIG api 99999999999999999999 1", "ERR bad arguments"},
		{0, "CONFIG api 1000000 1000000", "OK"},
		{0, "CONFIG api 007 002", "OK"},
	} {
		if got := s.exec(c.in, t0.Add(c.at)); got != c.want {
			t.Errorf("%q at %v: got %q, want %q", c.in, c.at, got, c.want)
		}
	}
}

func TestMaxLimiters(t *testing.T) {
	s := newServer()
	for i := range maxLimiters {
		if got := s.exec(fmt.Sprintf("CONFIG l%d 1 1", i), time.Now()); got != "OK" {
			t.Fatalf("limiter %d: got %q", i, got)
		}
	}
	if got := s.exec("CONFIG extra 1 1", time.Now()); got != "ERR too many limiters" {
		t.Fatalf("extra limiter: got %q", got)
	}
	if got := s.exec("CONFIG l0 2 2", time.Now()); got != "OK" {
		t.Fatalf("update existing: got %q", got)
	}
}

func TestSweep(t *testing.T) {
	s := newServer()
	t0 := time.Now()
	s.exec("CONFIG api 2 1", t0)
	s.exec("TAKE api old", t0)
	s.exec("TAKE api new", t0.Add(time.Second))
	s.sweep(t0.Add(2 * time.Second))
	b := s.limiters["api"].buckets
	if b["old"] != nil || b["new"] == nil {
		t.Fatalf("after sweep: old=%v new=%v", b["old"], b["new"])
	}
}

func TestMaxConns(t *testing.T) {
	defer func(n int) { maxConns = n }(maxConns)
	maxConns = 1
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serve(ln, time.Minute, log.New(io.Discard, "", 0))
	ping := func() string {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(c, "PING\n")
		got, _ := bufio.NewReader(c).ReadString('\n')
		c.Close()
		return got
	}
	first, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	first.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(first)
	io.WriteString(first, "PING\n")
	if got, _ := r.ReadString('\n'); got != "PONG\n" {
		t.Fatalf("first: got %q", got)
	}
	if got := ping(); got != "ERR too many connections\n" {
		t.Fatalf("second: got %q", got)
	}
	first.Close()
	for i := 0; ping() != "PONG\n"; i++ {
		if i == 50 {
			t.Fatal("slot not released after close")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var logBuf bytes.Buffer
	lf := &logFile{w: bufio.NewWriter(&logBuf)}
	go serve(ln, time.Minute, log.New(lf.writer(false), "", 0))
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

	io.WriteString(c, "PING\r\nCONFIG api 3 1\nTAKE api k\r\n")
	expect("PONG")
	expect("OK")
	expect("ALLOW 2")

	io.WriteString(c, "PING\nPI")
	expect("PONG")
	io.WriteString(c, "NG\n")
	expect("PONG")

	io.WriteString(c, "FAKE\rline\n")
	expect("ERR unknown command")

	io.WriteString(c, strings.Repeat("A", maxLine)+"\n")
	expect("ERR unknown command")
	io.WriteString(c, strings.Repeat("A", maxLine)+"\r\n")
	expect("ERR unknown command")

	io.WriteString(c, strings.Repeat("A", maxLine+100)+"\n")
	expect("ERR line too long")
	if _, err := r.ReadString('\n'); err != io.EOF {
		t.Fatalf("want EOF after line too long, got %v", err)
	}

	lf.writer(true).Write(nil)
	lf.mu.Lock()
	logged := logBuf.String()
	lf.mu.Unlock()
	for _, want := range []string{
		" connected\n",
		` "PING" PONG` + "\n",
		` "TAKE api k" ALLOW 2` + "\n",
		` "FAKE\rline" ERR unknown command` + "\n",
		" line too long\n",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log is missing %q:\n%s", want, logged)
		}
	}
}
