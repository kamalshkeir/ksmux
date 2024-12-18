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
	originalHost := req.Host
	originalScheme := "http"
	if req.TLS != nil {
		originalScheme = "https"
	}

	proxy.Director = func(r *http.Request) {
		r.URL.Host = url.Host
		r.URL.Scheme = url.Scheme

		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/*path")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}

		r.Host = url.Host
		r.Header.Set("X-Forwarded-Host", originalHost)
		r.Header.Set("X-Forwarded-Proto", originalScheme)

		for k, v := range req.Header {
			r.Header[k] = v
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		lg.Error("proxy error occurred",
			"err", err,
			"url", url.String(),
			"path", r.URL.Path,
			"method", r.Method)
		http.Error(w, "Proxy Error", http.StatusBadGateway)
	}

	proxy.ModifyResponse = func(r *http.Response) error {
		r.Header.Set("X-Proxied-By", "KSMUX")
		return nil
	}

	proxy.ServeHTTP(resp, req)
}

func proxyMid() func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if hostWithoutPort, _, err := net.SplitHostPort(host); err == nil {
				host = hostWithoutPort
			}
			host = strings.TrimSuffix(host, ":")

			if v, ok := proxies.Get(host); ok {
				v.ServeHTTP(w, r)
				return
			}
			h.ServeHTTP(w, r)
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
		host = host[:in]
		lg.WarnC("Port is ignored in host")
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

	handler := func(c *Context) {
		proxyHandler(c.Request, c.ResponseWriter, proxy, urll)
	}

	newRouter.Get("/", handler)
	newRouter.Post("/", handler)
	newRouter.Put("/", handler)
	newRouter.Delete("/", handler)
	newRouter.Patch("/", handler)
	newRouter.Options("/", handler)
	newRouter.Head("/", handler)

	newRouter.Get("/*path", handler)
	newRouter.Post("/*path", handler)
	newRouter.Put("/*path", handler)
	newRouter.Delete("/*path", handler)
	newRouter.Patch("/*path", handler)
	newRouter.Options("/*path", handler)
	newRouter.Head("/*path", handler)

	return newRouter
}
