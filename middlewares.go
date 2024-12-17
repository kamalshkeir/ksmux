package ksmux

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
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

var Cors = func(allowed ...string) func(http.Handler) http.Handler {
	corsEnabled = true
	if len(allowed) == 0 {
		allowed = append(allowed, "*")
	}
	for i := range allowed {
		if allowed[i] == "*" {
			continue
		}
		allowed[i] = strings.ReplaceAll(allowed[i], "localhost", "127.0.0.1")
		if !strings.HasPrefix(allowed[i], "http") {
			allowed[i] = "http://" + allowed[i]
		}
	}
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowed[0] == "*" {
				w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			} else {
				w.Header().Set("Access-Control-Allow-Origin", allowed[0])
			}
			w.Header().Set("Access-Control-Allow-Methods", "*")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Allow-Headers", "Access-Control-Allow-Headers, Origin, Accept, X-Requested-With, Content-Type, Access-Control-Request-Method, Access-Control-Request-Headers, X-Korm, Authorization, Token, X-Token")
				w.WriteHeader(http.StatusNoContent)
				return
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
