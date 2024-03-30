// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

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
	req.Host = url.Host
	req.URL.Host = url.Host
	req.URL.Scheme = url.Scheme
	//path := req.URL.Path
	//req.URL.Path = strings.TrimLeft(path, reverseProxyRoutePrefix)
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
