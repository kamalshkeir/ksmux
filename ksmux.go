// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"sync"

	"github.com/kamalshkeir/klog"
	"github.com/kamalshkeir/ksmux/ws"
)

func UpgradeConnection(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*ws.Conn, error) {
	return ws.DefaultUpgraderKSMUX.Upgrade(w, r, responseHeader)
}

// Handler is a function that can be registered to a route to handle HTTP
// requests. Like http.HandlerFunc, but has a third parameter for the values of
// wildcards (path variables).
type Handler func(c *Context)

type GroupRouter struct {
	*Router
	Group string
	midws []func(Handler) Handler
}

// Use chain handler middlewares
func (gr *GroupRouter) Use(middlewares ...func(Handler) Handler) {
	gr.midws = append(gr.midws, middlewares...)
}

// Group create group path
func (router *Router) Group(prefix string) *GroupRouter {
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return &GroupRouter{
		Router: router,
		Group:  prefix,
	}
}

// Use chain global router middlewares
func (router *Router) Use(midws ...func(http.Handler) http.Handler) {
	if len(router.middlewares) == 0 {
		router.middlewares = midws
	} else {
		router.middlewares = append(router.middlewares, midws...)
	}
}

// Router is a http.Handler which can be used to dispatch requests to different
// handler functions via configurable routes
type Router struct {
	Server *http.Server
	trees  map[string]*node

	paramsPool  sync.Pool
	maxParams   uint16
	middlewares []func(http.Handler) http.Handler
	// If enabled, adds the matched route path onto the http.Request context
	// before invoking the handler.
	// The matched route path is only added to handlers of routes that were
	// registered when this option was enabled.
	SaveMatchedPath bool

	// Enables automatic redirection if the current route can't be matched but a
	// handler for the path with (without) the trailing slash exists.
	// For example if /foo/ is requested but a route only exists for /foo, the
	// client is redirected to /foo with http status code 301 for GET requests
	// and 308 for all other request methods.
	RedirectTrailingSlash bool

	// If enabled, the router tries to fix the current request path, if no
	// handlre is registered for it.
	// First superfluous path elements like ../ or // are removed.
	// Afterwards the router does a case-insensitive lookup of the cleaned path.
	// If a handler can be found for this route, the router makes a redirection
	// to the corrected path with status code 301 for GET requests and 308 for
	// all other request methods.
	// For example /FOO and /..//Foo could be redirected to /foo.
	// RedirectTrailingSlash is independent of this option.
	RedirectFixedPath bool

	// If enabled, the router checks if another method is allowed for the
	// current route, if the current request can not be routed.
	// If this is the case, the request is answered with 'Method Not Allowed'
	// and HTTP status code 405.
	// If no other Method is allowed, the request is delegated to the NotFound
	// handler.
	HandleMethodNotAllowed bool

	// If enabled, the router automatically replies to OPTIONS requests.
	// Custom OPTIONS handlers take priority over automatic replies.
	HandleOPTIONS bool

	// An optional http.Handler that is called on automatic OPTIONS requests.
	// The handler is only called if HandleOPTIONS is true and no OPTIONS
	// handler for the specific path was set.
	// The "Allowed" header is set before calling the handler.
	GlobalOPTIONS http.Handler

	// Cached value of global (*) allowed methods
	globalAllowed string

	// Configurable http.Handler which is called when no matching route is
	// found. If it is not set, http.NotFound is used.
	NotFound http.Handler

	// Configurable http.Handler which is called when a request
	// cannot be routed and HandleMethodNotAllowed is true.
	// If it is not set, http.Error with http.StatusMethodNotAllowed is used.
	// The "Allow" header with allowed request methods is set before the handler
	// is called.
	MethodNotAllowed http.Handler

	// Function to handle panics recovered from http handlers.
	// It should be used to generate a error page and return the http error code
	// 500 (Internal Server Error).
	// The handler can be used to keep your server from crashing because of
	// unrecovered panics.
	PanicHandler func(http.ResponseWriter, *http.Request, interface{})
}

// New returns a new initialized Router.
// Path auto-correction, including trailing slashes, is enabled by default.
func New() *Router {
	return &Router{
		RedirectTrailingSlash:  true,
		RedirectFixedPath:      true,
		HandleMethodNotAllowed: true,
		HandleOPTIONS:          true,
	}
}

// Get is a shortcut for router.Handle(http.MethodGet, path, handler)
func (r *Router) Get(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodGet, path, handler)
}

func (gr *GroupRouter) Get(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodGet, gr.Group+pattern, handler)
}

// Head is a shortcut for router.Handle(http.MethodHead, path, handler)
func (r *Router) Head(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodHead, path, handler)
}

func (gr *GroupRouter) Head(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodHead, gr.Group+pattern, handler)
}

// Options is a shortcut for router.Handle(http.MethodOptions, path, handler)
func (r *Router) Options(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodOptions, path, handler)
}

func (gr *GroupRouter) Options(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodOptions, gr.Group+pattern, handler)
}

// Post is a shortcut for router.Handle(http.MethodPost, path, handler)
func (r *Router) Post(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodPost, path, handler)
}

func (gr *GroupRouter) Post(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodPost, gr.Group+pattern, handler)
}

// Put is a shortcut for router.Handle(http.MethodPut, path, handler)
func (r *Router) Put(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodPut, path, handler)
}

func (gr *GroupRouter) Put(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodPut, gr.Group+pattern, handler)
}

// Patch is a shortcut for router.Handle(http.MethodPatch, path, handler)
func (r *Router) Patch(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodPatch, path, handler)
}

func (gr *GroupRouter) Patch(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodPatch, gr.Group+pattern, handler)
}

// Delete is a shortcut for router.Handle(http.MethodDelete, path, handler)
func (r *Router) Delete(path string, handler Handler) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.Handle(http.MethodDelete, path, handler)
}

func (gr *GroupRouter) Delete(pattern string, handler Handler) {
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	var h Handler
	if len(gr.midws) > 0 {
		for i := range gr.midws {
			if i == 0 {
				h = gr.midws[0](handler)
			} else {
				h = gr.midws[i](h)
			}
		}
	} else {
		h = handler
	}
	gr.Router.Handle(http.MethodDelete, gr.Group+pattern, handler)
}

// Handle registers a new request handler with the given path and method.
//
// For GET, POST, PUT, PATCH and DELETE requests the respective shortcut
// functions can be used.
//
// This function is intended for bulk loading and to allow the usage of less
// frequently used, non-standardized or custom methods (e.g. for internal
// communication with a proxy).
func (r *Router) Handle(method, path string, handler Handler) {
	varsCount := uint16(0)

	if method == "" {
		klog.Printf("rdmethod cannot be empty for %s\n", path)
		return
	}
	if handler == nil {
		klog.Printf("rdmissing handler for %s\n", path)
		return
	}

	if r.SaveMatchedPath {
		varsCount++
		handler = r.saveMatchedRoutePath(path, handler)
	}

	if withDocs && !strings.Contains(path, "*") && method != "WS" && method != "SSE" {
		d := &DocsRoute{
			Pattern:     path,
			Summary:     "A " + method + " request on " + path,
			Description: "A " + method + " request on " + path,
			Method:      strings.ToLower(method),
			Accept:      "json",
			Produce:     "json",
			Params:      []string{},
		}
		docsPatterns = append(docsPatterns, &Route{
			Method:  method,
			Pattern: path,
			Handler: handler,
			Docs:    d,
		})
	}

	if r.trees == nil {
		r.trees = make(map[string]*node)
	}

	root := r.trees[method]
	if root == nil {
		root = new(node)
		r.trees[method] = root

		r.globalAllowed = r.allowed("*", "")
	}

	root.addPath(path, handler)

	// Update maxParams
	if paramsCount := countParams(path); paramsCount+varsCount > r.maxParams {
		r.maxParams = paramsCount + varsCount
	}

	// Lazy-init paramsPool alloc func
	if r.paramsPool.New == nil && r.maxParams > 0 {
		r.paramsPool.New = func() interface{} {
			ps := make(Params, 0, r.maxParams)
			return &ps
		}
	}
}

// HandlerFunc is an adapter which allows the usage of an http.HandlerFunc as a
// request handler.
func (r *Router) HandlerFunc(method, path string, handler http.HandlerFunc) {
	r.Handle(method, path, func(c *Context) {
		if len(c.params) > 0 {
			ctx := c.Request.Context()
			ctx = context.WithValue(ctx, ctxKey, c.params)
			c.Request = c.Request.WithContext(ctx)
		}
		handler.ServeHTTP(c.ResponseWriter, c.Request)
	},
	)
}

func (handler Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := contextPool.Get().(*Context)
	ctx.ResponseWriter = w
	ctx.Request = r
	ctx.params = GetParamsFromCtx(r.Context())
	handler(ctx)
	ctx.reset()
	contextPool.Put(ctx)
}

// ServeFiles serves files from the given file system root.
// The path must end with "/*filepath", files are then served from the local
// path /defined/root/dir/*filepath.
// For example if root is "/etc" and *filepath is "passwd", the local file
// "/etc/passwd" would be served.
// Internally a http.FileServer is used, therefore http.NotFound is used instead
// of the Router's NotFound handler.
// To use the operating system's file system implementation,
// use http.Dir:
//
//	router.ServeFiles("/src/*filepath", http.Dir("/var/www"))
func (r *Router) ServeFiles(path string, root http.FileSystem) {
	if len(path) < 10 || path[len(path)-10:] != "/*filepath" {
		klog.Printf("rdpath must end with /*filepath in path, path: %s\n", path)
		return
	}

	fileServer := http.FileServer(root)

	r.Get(path, func(c *Context) {
		c.Request.URL.Path = c.Param("filepath")
		fileServer.ServeHTTP(c.ResponseWriter, c.Request)
	})
}

// WithPprof enable std library pprof at /debug/pprof, prefix default to 'debug'
func (router *Router) WithPprof(path ...string) {
	if len(path) > 0 && strings.Contains(path[0], "/") {
		path[0] = strings.TrimPrefix(path[0], "/")
		path[0] = strings.TrimSuffix(path[0], "/")
	} else {
		path = append(path, "debug")
	}
	handler := func(c *Context) {
		ty := c.Param("type")
		switch ty {
		case "pprof", "":
			pprof.Index(c.ResponseWriter, c.Request)
			return
		case "profile":
			pprof.Profile(c.ResponseWriter, c.Request)
			return
		case "trace":
			pprof.Trace(c.ResponseWriter, c.Request)
			return
		default:
			pprof.Handler(ty).ServeHTTP(c.ResponseWriter, c.Request)
			return
		}
	}
	router.Get("/"+path[0]+"/:type", handler)
}

// WithMetrics take prometheus handler and serve metrics on path or default /metrics
func (router *Router) WithMetrics(httpHandler http.Handler, path ...string) {
	if len(path) > 0 && strings.Contains(path[0], "/") {
		path[0] = strings.TrimPrefix(path[0], "/")
	} else {
		path = append(path, "metrics")
	}

	router.Get("/"+path[0], func(c *Context) {
		httpHandler.ServeHTTP(c.ResponseWriter, c.Request)
	})
}

// WithDocs check and install swagger, and generate json and go docs at the end , after the server run, you can use kmux.OnDocsGenerationReady()
// genGoDocs default to true if genJsonDocs
func (router *Router) WithDocs(genJsonDocs bool, genGoDocs ...bool) *Router {
	withDocs = true
	generateSwaggerJson = genJsonDocs
	if len(genGoDocs) > 0 && !genGoDocs[0] {
		generateGoComments = false
	}
	if !swagFound && genJsonDocs {
		err := CheckAndInstallSwagger()
		if klog.CheckError(err) {
			return router
		}
	}
	return router
}

// ServeHTTP makes the router implement the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.PanicHandler != nil {
		defer r.recv(w, req)
	}

	path := req.URL.Path

	if root := r.trees[req.Method]; root != nil {
		if handler, ps, tsr := root.getHandler(path, r.getParams); handler != nil {
			c := contextPool.Get().(*Context)
			c.ResponseWriter = w
			c.Request = req
			if ps != nil {
				c.params = *ps
				handler(c)
				r.putParams(ps)
				c.reset()
			} else {
				handler(c)
				c.reset()
			}
			contextPool.Put(c)
			return
		} else if req.Method != http.MethodConnect && path != "/" {
			// Moved Permanently, request with GET method
			code := http.StatusMovedPermanently
			if req.Method != http.MethodGet {
				// Permanent Redirect, request with same method
				code = http.StatusPermanentRedirect
			}

			if tsr && r.RedirectTrailingSlash {
				if len(path) > 1 && path[len(path)-1] == '/' {
					req.URL.Path = path[:len(path)-1]
				} else {
					req.URL.Path = path + "/"
				}
				http.Redirect(w, req, req.URL.String(), code)
				return
			}

			// Try to fix the request path
			if r.RedirectFixedPath {
				fixedPath, found := root.getCaseInsensitivePath(
					cleanPath(path),
					r.RedirectTrailingSlash,
				)
				if found {
					req.URL.Path = fixedPath
					http.Redirect(w, req, req.URL.String(), code)
					return
				}
			}
		}
	}

	if req.Method == http.MethodOptions && r.HandleOPTIONS {
		// Handle OPTIONS requests
		if allow := r.allowed(path, http.MethodOptions); allow != "" {
			w.Header().Set("Allow", allow)
			if r.GlobalOPTIONS != nil {
				r.GlobalOPTIONS.ServeHTTP(w, req)
			}
			return
		}
	} else if r.HandleMethodNotAllowed { // Handle 405
		if allow := r.allowed(path, req.Method); allow != "" {
			w.Header().Set("Allow", allow)
			if r.MethodNotAllowed != nil {
				r.MethodNotAllowed.ServeHTTP(w, req)
			} else {
				http.Error(w,
					http.StatusText(http.StatusMethodNotAllowed),
					http.StatusMethodNotAllowed,
				)
			}
			return
		}
	}

	// Handle 404
	if r.NotFound != nil {
		r.NotFound.ServeHTTP(w, req)
	} else {
		http.NotFound(w, req)
	}
}

func (router *Router) initServer(addr string) {
	if addr != ADDRESS {
		ADDRESS = addr
	}
	var h http.Handler
	if len(router.middlewares) > 0 {
		for i := range router.middlewares {
			if i == 0 {
				h = router.middlewares[0](router)
			} else {
				h = router.middlewares[i](h)
			}
		}
	} else {
		h = router
	}

	server := http.Server{
		Addr:         addr,
		Handler:      h,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
	}
	router.Server = &server
}

// Run HTTP server on address
func (router *Router) Run(addr string) {
	if ADDRESS != addr {
		sp := strings.Split(addr, ":")
		if len(sp) > 0 {
			if sp[0] != "" && sp[1] != "" {
				ADDRESS = addr
			} else {
				HOST = "localhost"
				PORT = sp[1]
				ADDRESS = HOST + addr
			}
		} else {
			fmt.Println("error: server address not valid")
			return
		}
	}

	router.initServer(ADDRESS)

	// Listen and serve
	go func() {
		if err := router.Server.ListenAndServe(); err != http.ErrServerClosed {
			klog.Printf("rdUnable to shutdown the server : %v\n", err)
			os.Exit(1)
		} else {
			klog.Printfs("grServer Off!\n")
		}
	}()

	if generateSwaggerJson {
		DocsGeneralDefaults.Host = ADDRESS
		for i := len(docsPatterns) - 1; i >= 0; i-- {
			route := docsPatterns[i]
			if route.Docs == nil || route.Docs.Triggered || route.Method == "SSE" || route.Method == "WS" {
				docsPatterns = append(docsPatterns[:i], docsPatterns[i+1:]...)
			}
		}
		if generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}
	klog.Printfs("mgrunning on http://%s\n", ADDRESS)
	// graceful Shutdown server
	router.gracefulShutdown()
}

// RunTLS HTTPS server using certificates
func (router *Router) RunTLS(addr, cert, certKey string) {
	if ADDRESS != addr {
		sp := strings.Split(addr, ":")
		if len(sp) > 0 {
			if sp[0] != "" && sp[1] != "" {
				ADDRESS = addr
			} else {
				HOST = "localhost"
				PORT = sp[1]
				ADDRESS = HOST + addr
			}
		} else {
			fmt.Println("error: server address not valid")
			return
		}
	}
	IsTLS = true
	// graceful Shutdown server
	router.initServer(ADDRESS)

	go func() {
		klog.Printfs("mgrunning on https://%s\n", ADDRESS)
		if err := router.Server.ListenAndServeTLS(cert, certKey); err != http.ErrServerClosed {
			klog.Printf("rdUnable to shutdown the server : %v\n", err)
		} else {
			klog.Printfs("grServer Off!\n")
		}
	}()
	if generateSwaggerJson {
		DocsGeneralDefaults.Host = ADDRESS
		for i := len(docsPatterns) - 1; i >= 0; i-- {
			route := docsPatterns[i]
			if route.Docs == nil || route.Docs.Triggered || route.Method == "SSE" || route.Method == "WS" {
				docsPatterns = append(docsPatterns[:i], docsPatterns[i+1:]...)
			}
		}
		if generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}
	router.gracefulShutdown()
}

// RunAutoTLS HTTPS server generate certificates and handle renew
func (router *Router) RunAutoTLS(domainName string, subdomains ...string) {
	if !strings.Contains(domainName, ":") {
		err := checkDomain(domainName)
		if err == nil {
			DOMAIN = domainName
			ADDRESS = domainName
			PORT = "443"
		}
	} else {
		sp := strings.Split(domainName, ":")
		if sp[0] != "" {
			DOMAIN = sp[0]
			PORT = sp[1]
		}
	}
	IsTLS = true
	if proxyUsed {
		if len(SUBDOMAINS) != proxies.Len() {
			SUBDOMAINS = proxies.Keys()
		}
	}

	for _, d := range subdomains {
		found := false
		for _, dd := range SUBDOMAINS {
			if dd == d {
				found = true
			}
		}
		if !found {
			SUBDOMAINS = append(SUBDOMAINS, d)
		}
	}
	// graceful Shutdown server
	certManager, tlsconf := router.createServerCerts(DOMAIN, SUBDOMAINS...)
	if certManager == nil || tlsconf == nil {
		klog.Printf("rdunable to create tls config\n")
		os.Exit(1)
		return
	}
	router.initAutoServer(tlsconf)
	go http.ListenAndServe(":80", certManager.HTTPHandler(nil))
	go func() {
		klog.Printfs("mgrunning on https://%s , subdomains: %v\n", domainName, SUBDOMAINS)
		if err := router.Server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			klog.Printf("rdUnable to run the server : %v\n", err)
		} else {
			klog.Printfs("grServer Off !\n")
		}
	}()
	if generateSwaggerJson {
		DocsGeneralDefaults.Host = ADDRESS
		for i := len(docsPatterns) - 1; i >= 0; i-- {
			route := docsPatterns[i]
			if route.Docs == nil || route.Docs.Triggered || route.Method == "SSE" || route.Method == "WS" {
				docsPatterns = append(docsPatterns[:i], docsPatterns[i+1:]...)
			}
		}
		if generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}
	router.gracefulShutdown()
}

func (router *Router) initAutoServer(tlsconf *tls.Config) {
	var h http.Handler
	if len(router.middlewares) > 0 {
		for i := range router.middlewares {
			if i == 0 {
				h = router.middlewares[0](router)
			} else {
				h = router.middlewares[i](h)
			}
		}
	} else {
		h = router
	}
	// Setup Server
	server := http.Server{
		Addr:         ":" + PORT,
		Handler:      h,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
		TLSConfig:    tlsconf,
	}
	router.Server = &server
}
