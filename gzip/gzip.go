// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gzip

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"path"
	"strings"
)

func isSSEConnection(r *http.Request) bool {
	return r.Header.Get("Accept") == "text/event-stream"
}

func isWebSocket(r *http.Request) bool {
	for _, header := range r.Header["Upgrade"] {
		if header == "websocket" {
			return true
		}
	}
	return false
}

func isStaticFile(path string) bool {
	return strings.HasSuffix(path, ".js") ||
		strings.HasSuffix(path, ".css") ||
		strings.HasSuffix(path, ".png") ||
		strings.HasSuffix(path, ".jpg") ||
		strings.HasSuffix(path, ".jpeg") ||
		strings.HasSuffix(path, ".gif") ||
		strings.HasSuffix(path, ".svg") ||
		strings.HasSuffix(path, ".webp") ||
		strings.HasSuffix(path, ".ico") ||
		strings.HasSuffix(path, ".woff") ||
		strings.HasSuffix(path, ".woff2") ||
		strings.HasSuffix(path, ".ttf") ||
		strings.HasSuffix(path, ".eot") ||
		strings.HasSuffix(path, ".otf") ||
		strings.HasSuffix(path, ".mp4") ||
		strings.HasSuffix(path, ".webm") ||
		strings.HasSuffix(path, ".mp3") ||
		strings.HasSuffix(path, ".wav") ||
		strings.HasSuffix(path, ".ogg") ||
		strings.HasSuffix(path, ".pdf")
}

func GZIP(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip compression for special protocols
		if strings.Contains(r.URL.Path, "metrics") || strings.Contains(r.URL.Path, "trac") || strings.Contains(r.URL.Path, "monitor") ||
			isSSEConnection(r) ||
			isWebSocket(r) {
			handler.ServeHTTP(w, r)
			return
		}

		// For HTTP/2 static files, let the browser handle compression
		if r.ProtoMajor == 2 && isStaticFile(r.URL.Path) {
			// Set proper content type headers
			ext := strings.ToLower(path.Ext(r.URL.Path))
			switch ext {
			case ".js":
				w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			case ".css":
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			case ".png":
				w.Header().Set("Content-Type", "image/png")
			case ".jpg", ".jpeg":
				w.Header().Set("Content-Type", "image/jpeg")
			case ".gif":
				w.Header().Set("Content-Type", "image/gif")
			case ".svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			case ".webp":
				w.Header().Set("Content-Type", "image/webp")
			case ".ico":
				w.Header().Set("Content-Type", "image/x-icon")
			case ".woff":
				w.Header().Set("Content-Type", "font/woff")
			case ".woff2":
				w.Header().Set("Content-Type", "font/woff2")
			case ".ttf":
				w.Header().Set("Content-Type", "font/ttf")
			case ".eot":
				w.Header().Set("Content-Type", "application/vnd.ms-fontobject")
			case ".otf":
				w.Header().Set("Content-Type", "font/otf")
			case ".mp4":
				w.Header().Set("Content-Type", "video/mp4")
			case ".webm":
				w.Header().Set("Content-Type", "video/webm")
			case ".mp3":
				w.Header().Set("Content-Type", "audio/mpeg")
			case ".wav":
				w.Header().Set("Content-Type", "audio/wav")
			case ".ogg":
				w.Header().Set("Content-Type", "audio/ogg")
			case ".pdf":
				w.Header().Set("Content-Type", "application/pdf")
			}
			handler.ServeHTTP(w, r)
			return
		}

		// Apply gzip for everything else that accepts it
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			gwriter := NewWrappedResponseWriter(w)
			defer gwriter.Flush()
			gwriter.Header().Set("Content-Encoding", "gzip")
			handler.ServeHTTP(gwriter, r)
			return
		}

		handler.ServeHTTP(w, r)
	})
}

type WrappedResponseWriter struct {
	w       http.ResponseWriter
	gwriter *gzip.Writer
}

func NewWrappedResponseWriter(w http.ResponseWriter) *WrappedResponseWriter {
	gwriter := gzip.NewWriter(w)
	return &WrappedResponseWriter{w, gwriter}
}

func (wrw *WrappedResponseWriter) Header() http.Header {
	return wrw.w.Header()
}

func (wrw *WrappedResponseWriter) WriteHeader(statuscode int) {
	wrw.w.WriteHeader(statuscode)
}

func (wrw *WrappedResponseWriter) Write(d []byte) (int, error) {
	return wrw.gwriter.Write(d)
}

func (wrw *WrappedResponseWriter) Flush() {
	wrw.gwriter.Flush()
}

func (wrw *WrappedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := wrw.w.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("http.Hijacker interface is not supported")
}
