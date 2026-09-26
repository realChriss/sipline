package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"math"
	"net"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxLine = 1024

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu       sync.Mutex
	capacity float64
	rate     float64
	buckets  map[string]*bucket
}

type server struct {
	mu       sync.Mutex
	limiters map[string]*limiter
	log      *log.Logger
}

type logFile struct {
	mu sync.Mutex
	w  *bufio.Writer
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func (l *logFile) writer(flush bool) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		n, err := l.w.Write(p)
		if err == nil && flush {
			err = l.w.Flush()
		}
		return n, err
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7700", "TCP address to listen on")
	sweep := flag.Duration("sweep", 60*time.Second, "how often idle buckets are evicted")
	logPath := flag.String("log", "", "append logs, including every request, to this file")
	flag.Parse()
	reqLog := log.New(io.Discard, "", 0)
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		lf := &logFile{w: bufio.NewWriterSize(f, 64<<10)}
		flush := lf.writer(true)
		log.SetOutput(io.MultiWriter(os.Stderr, flush))
		reqLog = log.New(lf.writer(false), "", log.LstdFlags)
		go func() {
			for range time.Tick(time.Second) {
				flush.Write(nil)
			}
		}()
		go func() {
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
			<-stop
			flush.Write(nil)
			os.Exit(0)
		}()
	}
	if *sweep <= 0 {
		log.Fatal("--sweep must be positive")
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("sipline listening on %s", ln.Addr())
	log.Fatal(serve(ln, *sweep, reqLog))
}

func newServer() *server {
	return &server{limiters: map[string]*limiter{}, log: log.New(io.Discard, "", 0)}
}

func serve(ln net.Listener, sweep time.Duration, reqLog *log.Logger) error {
	s := newServer()
	s.log = reqLog
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
		if err != nil {
			log.Print(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr()
	s.log.Printf("%s connected", addr)
	defer s.log.Printf("%s disconnected", addr)
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		line, err := r.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull {
			return
		}
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if err == bufio.ErrBufferFull || len(line) > maxLine {
			break
		}
		reply := s.exec(string(line), time.Now())
		s.log.Printf("%s %q %s", addr, line, reply)
		w.WriteString(reply + "\n")
		if buf, _ := r.Peek(r.Buffered()); bytes.IndexByte(buf, '\n') < 0 && w.Flush() != nil {
			return
		}
	}
	s.log.Printf("%s line too long", addr)
	w.WriteString("ERR line too long\n")
	w.Flush()
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
		tc.SetReadDeadline(time.Now().Add(time.Second))
		io.Copy(io.Discard, tc)
	}
}

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
