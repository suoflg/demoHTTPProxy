package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server ...
type Server interface {
	Run() error
	Stop(duration time.Duration) error
}

type csMap struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *csMap) load(key string) (string, bool) {
	c.mu.Lock()
	value, ok := c.m[key]
	c.mu.Unlock()
	return value, ok
}

func (c *csMap) loadAndDelete(key string) (string, bool) {
	c.mu.Lock()
	value, ok := c.m[key]
	if ok {
		delete(c.m, key)
	}
	c.mu.Unlock()
	return value, ok
}

func (c *csMap) store(key, value string) {
	c.mu.Lock()
	c.m[key] = value
	c.mu.Unlock()
}

type server struct {
	rule   csMap
	bp     *sync.Pool
	srv    *http.Server
	dialer *net.Dialer
	tr     *http.Transport
}

func (s *server) Run() error {
	return s.srv.ListenAndServe()
}

func (s *server) Stop(wait time.Duration) error {
	if wait <= 0 {
		return s.srv.Shutdown(context.Background())
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func New(addr string) Server {
	s := &server{
		rule: csMap{m: make(map[string]string)},
		bp: &sync.Pool{
			New: func() interface{} {
				return make([]byte, 1024)
			},
		},
	}
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s,
	}
	s.dialer = &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	s.tr = &http.Transport{
		DialContext:           s.dial,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.findRule(r)
	if r.Method == http.MethodConnect {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "unsupported", http.StatusInternalServerError)
			return
		}

		addr := r.Host
		if addr == "" {
			addr = r.URL.Host
		}
		if r.URL.Port() != "" && !strings.HasSuffix(addr, r.URL.Port()) {
			addr += ":" + r.URL.Port()
		}

		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		_, err = clientConn.Write([]byte(fmt.Sprintf("HTTP/%d.%d 200 Connection established\r\n\r\n", r.ProtoMajor, r.ProtoMinor)))
		if err != nil {
			log.Printf("respond connect request failed: %s\n", err)
			_ = clientConn.Close()
			return
		}

		serverConn, err := s.dial(nil, "tcp", addr)
		if err != nil {
			log.Printf("dail remote addr failed: %s\n", err)
			_ = clientConn.Close()
			return
		}

		go s.copyAndClose(serverConn, clientConn)
		go s.copyAndClose(clientConn, serverConn)
	} else {
		rsp, err := s.tr.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = rsp.Body.Close() }()

		rsp.Header.Clone()
		for k, v := range rsp.Header {
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}

		w.WriteHeader(rsp.StatusCode)
		s.copy(w, rsp.Body)
	}
}

func (s *server) findRule(r *http.Request) {
	ua := r.UserAgent()

	const ruleHeader = "[RULE]"
	if len(ua) <= len(ruleHeader) || !strings.HasPrefix(ua, ruleHeader) {
		return
	}

	end := strings.IndexByte(ua, '$')
	if end == -1 {
		return
	}
	r.Header.Set("User-Agent", ua[end+1:])

	rule := strings.Split(ua[len(ruleHeader):end], "@")
	if len(rule) != 2 {
		return
	}
	s.rule.store(rule[0], rule[1])
}

func (s *server) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	newAddr, ok := s.rule.loadAndDelete(addr)
	if ok {
		log.Printf("%s ==> %s\n", addr, newAddr)
		addr = newAddr
	}

	if ctx == nil {
		return s.dialer.Dial(network, addr)
	}
	return s.dialer.DialContext(ctx, network, addr)
}

func (s *server) copyAndClose(dst io.WriteCloser, src io.ReadCloser) {
	defer func() {
		_ = dst.Close()
		_ = src.Close()
	}()

	s.copy(dst, src)
}

func (s *server) copy(dst io.Writer, src io.Reader) {
	buf := s.bp.Get().([]byte)
	_, _ = io.CopyBuffer(dst, src, buf)
	s.bp.Put(buf)
}
