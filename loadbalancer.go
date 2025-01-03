package ksmux

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	StateClosed = iota
	StateOpen
	StateHalfOpen
)

type Backend struct {
	URL            *url.URL
	Alive          bool
	mux            sync.RWMutex
	ReverseProxy   *httputil.ReverseProxy
	FailureCount   int32
	LastChecked    time.Time
	circuitBreaker *CircuitBreaker
	Middlewares    []Handler
}

type LoadBalancer struct {
	backends    []*Backend
	current     uint64
	mux         sync.RWMutex
	healthCheck bool
}

type CircuitBreaker struct {
	state            int32
	failureCount     int32
	lastFailure      time.Time
	failureThreshold int32
	resetTimeout     time.Duration
	mutex            sync.RWMutex
}

type BackendOpt struct {
	Url         string
	Middlewares []Handler
}

func (router *Router) LoadBalancer(pattern string, backends ...BackendOpt) error {
	lb := newLoadBalancer(true)
	if !strings.HasSuffix(pattern, "*") && !strings.HasSuffix(pattern, "/") {
		pattern = pattern + "/"
	}
	exact := pattern
	exact = strings.TrimSuffix(exact, "*")
	if strings.HasSuffix(pattern, "*") {
		pattern = pattern + "any"
	} else {
		pattern = pattern + "*any"
	}
	for _, backend := range backends {
		if err := lb.AddBackend(backend.Url, backend.Middlewares...); err != nil {
			return err
		}
	}

	handler := func(c *Context) {
		// Ignore favicon requests
		if strings.HasSuffix(c.Request.URL.Path, "favicon.ico") {
			c.Status(http.StatusOK)
			return
		}

		// Get the next backend
		backend := lb.NextBackend(c.Request.URL.Path)
		if backend == nil {
			c.Status(http.StatusServiceUnavailable).Text("No backend available")
			return
		}

		// Preserve the original request URL and headers
		originalURL := *c.Request.URL
		originalHost := c.Request.Host
		originalHeaders := make(http.Header)
		for k, v := range c.Request.Header {
			originalHeaders[k] = v
		}

		// Apply middlewares
		for _, middleware := range backend.Middlewares {
			middleware(c)
		}

		backend.ReverseProxy.ServeHTTP(c.ResponseWriter, c.Request)

		// Restore the original URL, host and headers
		c.Request.URL = &originalURL
		c.Request.Host = originalHost
		c.Request.Header = originalHeaders
	}
	router.Get(exact, handler)
	router.Post(exact, handler)
	router.Put(exact, handler)
	router.Delete(exact, handler)
	router.Patch(exact, handler)
	router.Options(exact, handler)
	router.Head(exact, handler)
	router.Get(pattern, handler)
	router.Post(pattern, handler)
	router.Put(pattern, handler)
	router.Delete(pattern, handler)
	router.Patch(pattern, handler)
	router.Options(pattern, handler)
	router.Head(pattern, handler)

	return nil
}

func newLoadBalancer(healthCheck bool) *LoadBalancer {
	return &LoadBalancer{
		backends:    make([]*Backend, 0),
		healthCheck: healthCheck,
	}
}

func (lb *LoadBalancer) AddBackend(rawURL string, mid ...Handler) error {
	if !strings.HasPrefix(rawURL, "http") {
		rawURL = "http://" + rawURL
	}
	url, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	// Configure the proxy
	proxy.Director = func(req *http.Request) {
		// Save original path
		path := req.URL.Path

		// Set target URL
		req.URL.Scheme = url.Scheme
		req.URL.Host = url.Host

		// Handle root path specially
		if path == "" || path == "/" {
			req.URL.Path = "/"
		} else {
			// Remove any double slashes and ensure path starts with /
			req.URL.Path = "/" + strings.TrimPrefix(path, "/")
		}

		// Copy headers
		if _, ok := req.Header["User-Agent"]; !ok {
			req.Header.Set("User-Agent", "")
		}

		// Set Host header to match target
		req.Host = url.Host

	}

	// Add error handling
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("Bad Gateway"))
	}

	// Configure transport
	proxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	backend := &Backend{
		URL:            url,
		Alive:          true,
		ReverseProxy:   proxy,
		circuitBreaker: NewCircuitBreaker(),
		Middlewares:    mid,
	}

	lb.mux.Lock()
	lb.backends = append(lb.backends, backend)
	lb.mux.Unlock()

	if lb.healthCheck {
		// Run initial health check synchronously
		lb.checkBackendHealth(backend)
		// Start background health checks
		go lb.healthCheckBackend(backend)
	}

	return nil
}

func (lb *LoadBalancer) checkBackendHealth(backend *Backend) {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	healthURL := *backend.URL
	healthURL.Path = "/health"

	resp, err := client.Get(healthURL.String())
	backend.mux.Lock()
	defer backend.mux.Unlock()

	if err != nil {
		backend.Alive = false
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		backend.Alive = true
		backend.circuitBreaker.RecordSuccess()
	} else {
		backend.Alive = false
		backend.circuitBreaker.RecordFailure()
	}
	backend.LastChecked = time.Now()
}

func (lb *LoadBalancer) healthCheckBackend(backend *Backend) {
	// Initial health check
	lb.checkBackendHealth(backend)

	ticker := time.NewTicker(5 * time.Second)
	for range ticker.C {
		lb.checkBackendHealth(backend)
	}
}

// Round Robin Selection
func (lb *LoadBalancer) NextBackend(path string) *Backend {
	lb.mux.RLock()
	defer lb.mux.RUnlock()

	if len(lb.backends) == 0 {
		return nil
	}

	count := len(lb.backends)
	idx := int(atomic.AddUint64(&lb.current, 1)-1) % count

	// Try the selected backend first
	backend := lb.backends[idx]
	if backend.Alive && backend.circuitBreaker.IsAvailable() {
		return backend
	}

	// If the selected backend is not available, try others
	for i := 1; i < count; i++ {
		tryIdx := (idx + i) % count
		backend := lb.backends[tryIdx]
		if backend.Alive && backend.circuitBreaker.IsAvailable() {
			return backend
		}
	}

	return nil
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: 5,
		resetTimeout:     time.Second,
	}
}

func (cb *CircuitBreaker) IsAvailable() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	if cb.state == StateOpen {
		// Check if enough time has passed to try again
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.mutex.RUnlock()
			cb.mutex.Lock()
			cb.state = StateHalfOpen
			cb.mutex.Unlock()
			cb.mutex.RLock()
			return true
		}
		return false
	}
	return true
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount = 0
	cb.state = StateClosed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	if cb.failureCount >= cb.failureThreshold {
		cb.state = StateOpen
		cb.lastFailure = time.Now()
	}
}
