// Command sipline is a localhost token-bucket rate limiter speaking a line-based TCP protocol.
// See SPEC.md for the protocol.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxLine = 1024

type bucket struct {
	tokens float64
	last   time.Time // monotonic reading from time.Now
}

type limiter struct {
	mu       sync.Mutex
	capacity float64
	rate     float64 // tokens per second
	buckets  map[string]*bucket
}

type server struct {
	mu       sync.Mutex
	limiters map[string]*limiter
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7700", "TCP address to listen on")
	sweep := flag.Duration("sweep", 60*time.Second, "how often idle buckets are evicted")
	flag.Parse()
	if *sweep <= 0 {
		log.Fatal("--sweep must be positive")
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("sipline listening on %s", ln.Addr())
	log.Fatal(serve(ln, *sweep))
}

func newServer() *server {
	return &server{limiters: map[string]*limiter{}}
}

// serve accepts connections until ln is closed.
func serve(ln net.Listener, sweep time.Duration) error {
	s := newServer()
	go func() {
		for now := range time.Tick(sweep) {
			s.sweep(now)
		}
	}()
	for {
		conn, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil { // e.g. out of file descriptors: back off instead of dying
			log.Print(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn) // ScanLines drops a trailing \r
	sc.Buffer(make([]byte, 0, maxLine+2), maxLine+2)
	w := bufio.NewWriter(conn)
	tooLong := false
	for sc.Scan() {
		if tooLong = len(sc.Bytes()) > maxLine; tooLong {
			break
		}
		w.WriteString(s.exec(sc.Text(), time.Now()) + "\n")
		// ponytail: flush per reply; flush only when no input is buffered if syscalls ever matter
		if w.Flush() != nil {
			return
		}
	}
	if !tooLong && !errors.Is(sc.Err(), bufio.ErrTooLong) {
		return
	}
	conn.Write([]byte("ERR line too long\n"))
	// Closing with unread input would send a RST and could discard the reply
	// before the client reads it, so half-close and drain briefly first.
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
		tc.SetReadDeadline(time.Now().Add(time.Second))
		io.Copy(io.Discard, tc)
	}
}

// exec runs one command line and returns the response without the newline.
func (s *server) exec(line string, now time.Time) string {
	const badArgs = "ERR bad arguments"
	f := strings.Split(line, " ")
	switch f[0] {
	case "PING":
		if len(f) != 1 {
			return badArgs
		}
		return "PONG"
	case "CONFIG":
		if len(f) != 4 || !validName(f[1]) {
			return badArgs
		}
		capacity, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || capacity <= 0 {
			return badArgs
		}
		rate, err := strconv.ParseFloat(f[3], 64)
		// Also reject rates so small that retry_ms would overflow to +Inf.
		if err != nil || !(rate > 0) || math.IsInf(rate, 0) || math.IsInf(1000/rate, 0) {
			return badArgs
		}
		s.config(f[1], float64(capacity), rate)
		return "OK"
	case "TAKE":
		if len(f) != 3 || !validName(f[1]) || !validName(f[2]) {
			return badArgs
		}
		s.mu.Lock()
		l := s.limiters[f[1]]
		s.mu.Unlock()
		if l == nil {
			return "ERR unknown limiter"
		}
		return l.take(f[2], now)
	}
	return "ERR unknown command"
}

// validName reports whether s is 1–256 bytes with no spaces or control characters.
func validName(s string) bool {
	if len(s) < 1 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// config creates or updates a limiter. Existing buckets are clamped to the
// new capacity lazily, on their next take.
func (s *server) config(name string, capacity, rate float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.limiters[name]
	if l == nil {
		l = &limiter{buckets: map[string]*bucket{}}
		s.limiters[name] = l
	}
	l.mu.Lock()
	l.capacity, l.rate = capacity, rate
	l.mu.Unlock()
}

func (l *limiter) take(key string, now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}
	// now can trail b.last slightly when concurrent callers race for the lock.
	if now.After(b.last) {
		b.tokens += now.Sub(b.last).Seconds() * l.rate
		b.last = now
	}
	b.tokens = min(b.tokens, l.capacity)
	if b.tokens >= 1 {
		b.tokens--
		return fmt.Sprintf("ALLOW %.0f", math.Floor(b.tokens))
	}
	return fmt.Sprintf("DENY %.0f", math.Ceil((1-b.tokens)/l.rate*1000))
}

// sweep drops buckets idle long enough to be full again; a returning key
// gets a fresh full bucket, which is identical.
func (s *server) sweep(now time.Time) {
	s.mu.Lock()
	ls := slices.Collect(maps.Values(s.limiters))
	s.mu.Unlock()
	for _, l := range ls {
		l.mu.Lock()
		for k, b := range l.buckets {
			if now.Sub(b.last).Seconds() >= l.capacity/l.rate {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}
