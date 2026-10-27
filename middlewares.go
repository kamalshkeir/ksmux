package ksmux

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kamalshkeir/ksmux/gzip"
	"github.com/kamalshkeir/lg"
)

func Gzip() func(http.Handler) http.Handler {
	return gzip.GZIP
}

func BasicAuth(ksmuxHandlerFunc Handler, user, pass string) Handler {
	return func(c *Context) {
		username, password, ok := c.Request.BasicAuth()
		if ok {
			usernameHash := sha256.Sum256([]byte(username))
			passwordHash := sha256.Sum256([]byte(password))
			if user == "" || pass == "" {
				c.ResponseWriter.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
				http.Error(c.ResponseWriter, "Unauthorized", http.StatusUnauthorized)
				return
			}
			expectedUsernameHash := sha256.Sum256([]byte(user))
			expectedPasswordHash := sha256.Sum256([]byte(pass))
			usernameMatch := (subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1)
			passwordMatch := (subtle.ConstantTimeCompare(passwordHash[:], expectedPasswordHash[:]) == 1)

			if usernameMatch && passwordMatch {
				ksmuxHandlerFunc(c)
				return
			}
		}
		c.ResponseWriter.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
		http.Error(c.ResponseWriter, "Unauthorized", http.StatusUnauthorized)
	}
}

func IgnoreLogsEndpoints(pathContain ...string) {
	if len(pathContain) > 0 {
		for _, p := range pathContain {
			found := false
			for _, s := range ignored {
				if p == s {
					found = true
				}
			}
			if !found {
				ignored = append(ignored, p)
			}
		}
	}
}

// Logs middleware log requests, and can execute one optional callback on each request
var ignored = []string{"/metrics", "sw.js", "favicon", "/static/", "/sse/", "/ws/", "/wss/"}

func Logs(callback ...func(method, path, remote string, status int, took time.Duration)) func(http.Handler) http.Handler {
	return func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			for _, ig := range ignored {
				if strings.Contains(r.URL.Path, ig) {
					handler.ServeHTTP(w, r)
					return
				}
			}
			//check if connection is ws
			for _, header := range r.Header["Upgrade"] {
				if header == "websocket" {
					// connection is ws
					handler.ServeHTTP(w, r)
					return
				}
			}
			recorder := &StatusRecorder{
				ResponseWriter: w,
				Status:         200,
			}
			t := time.Now()
			handler.ServeHTTP(recorder, r)
			took := time.Since(t)
			res := fmt.Sprintf("[%s] --> '%s' --> [%d]  from: %s ---------- Took: %v", r.Method, r.URL.Path, recorder.Status, r.RemoteAddr, took)

			if len(callback) > 0 {
				callback[0](r.Method, r.URL.Path, r.RemoteAddr, recorder.Status, took)
			}
			if recorder.Status >= 200 && recorder.Status < 400 {
				lg.Printfs("gr%s\n", res)
			} else if recorder.Status >= 400 || recorder.Status < 200 {
				lg.Printfs("%s\n", res)
			} else {
				lg.Printfs("yl%s\n", res)
			}
		})
	}
}

// CorsConfig holds configuration options for the CORS middleware.
type CorsConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// Cors returns a CORS middleware with the specified allowed origins.
// If no origins are provided or "*" is included, all origins are permitted.
var Cors = func(allowed ...string) func(http.Handler) http.Handler {
	return CorsWithConfig(CorsConfig{
		AllowedOrigins:   allowed,
		AllowCredentials: true,
	})
}

// CorsWithConfig returns a CORS middleware with customizable options.
func CorsWithConfig(cfg CorsConfig) func(http.Handler) http.Handler {
	if firstRouter != nil {
		firstRouter.state.corsEnabled = true
	}

	allowAll := len(cfg.AllowedOrigins) == 0
	var cleanOrigins []string
	for _, o := range cfg.AllowedOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowAll = true
			break
		}
		if o != "" {
			o = strings.TrimSuffix(o, "/")
			cleanOrigins = append(cleanOrigins, o)
		}
	}

	allowedMethods := "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS"
	if len(cfg.AllowedMethods) > 0 {
		allowedMethods = strings.Join(cfg.AllowedMethods, ", ")
	}

	exposedHeaders := ""
	if len(cfg.ExposedHeaders) > 0 {
		exposedHeaders = strings.Join(cfg.ExposedHeaders, ", ")
	}

	maxAgeStr := ""
	if cfg.MaxAge > 0 {
		maxAgeStr = strconv.Itoa(cfg.MaxAge)
	} else {
		maxAgeStr = "86400"
	}

	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if firstRouter != nil && !firstRouter.state.corsEnabled {
				firstRouter.state.corsEnabled = true
			}

			origin := r.Header.Get("Origin")
			originAllowed := false

			if allowAll {
				originAllowed = true
				if origin != "" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				} else {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				}
			} else if origin != "" {
				trimmedOrigin := strings.TrimSuffix(origin, "/")
				for _, o := range cleanOrigins {
					if strings.EqualFold(trimmedOrigin, o) ||
						strings.EqualFold(trimmedOrigin, "http://"+o) ||
						strings.EqualFold(trimmedOrigin, "https://"+o) {
						originAllowed = true
						w.Header().Set("Access-Control-Allow-Origin", origin)
						break
					}
				}
			}

			if originAllowed {
				w.Header().Add("Vary", "Origin")
				if cfg.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if exposedHeaders != "" {
					w.Header().Set("Access-Control-Expose-Headers", exposedHeaders)
				}
			}

			w.Header().Set("Access-Control-Allow-Methods", allowedMethods)

			// Handle Preflight OPTIONS
			if r.Method == http.MethodOptions {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")

				reqHeaders := r.Header.Get("Access-Control-Request-Headers")
				if len(cfg.AllowedHeaders) > 0 {
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
				} else if reqHeaders != "" {
					w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
				} else {
					w.Header().Set("Access-Control-Allow-Headers", "Origin, Accept, Content-Type, Authorization, X-Requested-With, Token, X-Token")
				}

				if maxAgeStr != "" {
					w.Header().Set("Access-Control-Max-Age", maxAgeStr)
				}

				if !originAllowed && !allowAll && origin != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}

				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Non-preflight requests
			if len(cfg.AllowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
			} else {
				reqHeaders := r.Header.Get("Access-Control-Request-Headers")
				if reqHeaders != "" {
					w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
				} else {
					w.Header().Set("Access-Control-Allow-Headers", "*")
				}
			}

			// Next
			h.ServeHTTP(w, r)
		})
	}
}

type StatusRecorder struct {
	http.ResponseWriter
	Status int
}

func (r *StatusRecorder) WriteHeader(status int) {
	r.Status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *StatusRecorder) Flush() {
	if v, ok := r.ResponseWriter.(http.Flusher); ok {
		v.Flush()
	}
}

func (r *StatusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("LOGS MIDDLEWARE: http.Hijacker interface is not supported")
}

// Add this variable near the top with other vars
var tracingIgnored = []string{"/metrics", "sw.js", "favicon", "/static/", "/sse/", "/ws/", "/wss/"}

// IgnoreTracingEndpoints allows adding paths to ignore in tracing
func IgnoreTracingEndpoints(pathContain ...string) {
	if len(pathContain) > 0 {
		for _, p := range pathContain {
			found := false
			for _, s := range tracingIgnored {
				if p == s {
					found = true
				}
			}
			if !found {
				tracingIgnored = append(tracingIgnored, p)
			}
		}
	}
}

// TracingMiddleware adds tracing to all routes
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if path should be ignored
		for _, ig := range tracingIgnored {
			if strings.Contains(r.URL.Path, ig) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Check if connection is ws
		for _, header := range r.Header["Upgrade"] {
			if header == "websocket" {
				next.ServeHTTP(w, r)
				return
			}
		}

		span, ctx := StartSpan(r.Context(), fmt.Sprintf("%s %s", r.Method, r.URL.Path))
		if span != nil {
			defer span.End()

			// Add common tags
			span.SetTag("http.method", r.Method)
			span.SetTag("http.url", r.URL.String())
			span.SetTag("http.remote_addr", r.RemoteAddr)
			span.SetTag("http.user_agent", r.UserAgent())

			// Create new context with span
			r = r.WithContext(ctx)

			// Wrap ResponseWriter to capture status code
			wrappedWriter := &responseWriterWithStatus{ResponseWriter: w}
			w = wrappedWriter

			// Execute handler
			next.ServeHTTP(w, r)

			// Record status code
			span.SetStatusCode(wrappedWriter.status)
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// responseWriterWithStatus wraps http.ResponseWriter to capture status code
type responseWriterWithStatus struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriterWithStatus) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Add these methods to properly implement http.ResponseWriter interfaces
func (rw *responseWriterWithStatus) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacking not supported")
}

func (rw *responseWriterWithStatus) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriterWithStatus) Push(target string, opts *http.PushOptions) error {
	if p, ok := rw.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
