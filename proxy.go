package ksmux

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/kamalshkeir/lg"
)

func proxyHandler(req *http.Request, resp http.ResponseWriter, proxy *httputil.ReverseProxy, url *url.URL) {
	// Store original host
	originalHost := req.Host
	originalScheme := "http"
	if req.TLS != nil {
		originalScheme = "https"
	}

	// Create director function to modify request
	proxy.Director = func(r *http.Request) {
		r.URL.Host = url.Host
		r.URL.Scheme = url.Scheme

		// Preserve original host
		r.Header.Set("X-Forwarded-Host", originalHost)

		// Preserve client IP
		if clientIP := req.Header.Get("X-Forwarded-For"); clientIP != "" {
			r.Header.Set("X-Forwarded-For", clientIP)
		} else if clientIP := req.Header.Get("X-Real-IP"); clientIP != "" {
			r.Header.Set("X-Forwarded-For", clientIP)
		} else {
			ip, _, err := net.SplitHostPort(req.RemoteAddr)
			if err == nil {
				r.Header.Set("X-Forwarded-For", ip)
			}
		}

		// Preserve original scheme
		r.Header.Set("X-Forwarded-Proto", originalScheme)

		// Pass through all original headers including auth
		for key, values := range req.Header {
			r.Header[key] = values
		}
	}

	// Add error handling
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		lg.Error("proxy error", "err", err, "url", url.String())
		http.Error(w, "Proxy Error", http.StatusBadGateway)
	}

	// Modify response headers
	proxy.ModifyResponse = func(r *http.Response) error {
		// Add proxy identifier
		r.Header.Set("X-Proxied-By", "KSMUX")
		return nil
	}

	proxy.ServeHTTP(resp, req)
}

func proxyMid() func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.Host)
			if lg.CheckError(err) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if v, ok := proxies.Get(host); ok {
				if vv, ok := v.(*Router); ok {
					for _, mid := range vv.middlewares {
						mid(v).ServeHTTP(w, r)
					}
				}
				v.ServeHTTP(w, r)
			} else {
				h.ServeHTTP(w, r)
			}
		})
	}
}

func (router *Router) ReverseProxy(host, toURL string) (newRouter *Router) {
	urll, err := url.Parse(toURL)
	if lg.CheckError(err) {
		return
	}
	if strings.Contains(host, "*") {
		lg.ErrorC("contain wildcard, Not Allowed", "host", host)
		return
	}

	if strings.Contains(host, "/") {
		lg.ErrorC("contain slash symbol '/', Not Allowed", "host", host)
		return
	}
	if in := strings.Index(host, ":"); in > -1 {
		lg.WarnC("Port is ignored inhost")
		host = host[:in]
	}
	proxy := httputil.NewSingleHostReverseProxy(urll)
	if !proxyUsed {
		proxyUsed = true
		if len(router.middlewares) > 0 {
			router.middlewares = append([]func(http.Handler) http.Handler{proxyMid()}, router.middlewares...)
		} else {
			router.middlewares = append(router.middlewares, proxyMid())
		}
	}
	newRouter = New()
	_ = proxies.Set(host, newRouter)

	newRouter.Get("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Post("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Patch("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Put("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Delete("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Options("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	newRouter.Head("/*anyrp", func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	})
	return newRouter
}
