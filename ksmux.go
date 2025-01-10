package ksmux

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/kamalshkeir/kmap"
	"github.com/kamalshkeir/ksmux/ws"
	"github.com/kamalshkeir/lg"
	"golang.org/x/crypto/acme/autocert"
)

var allrouters = kmap.New[string, *Router]()

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

type RouterConfig struct {
	ReadTimeout            time.Duration
	WriteTimeout           time.Duration
	IdleTimeout            time.Duration
	GlobalOPTIONS          http.Handler
	NotFound               http.Handler
	MethodNotAllowed       http.Handler
	PanicHandler           func(http.ResponseWriter, *http.Request, interface{})
	globalAllowed          string
	SaveMatchedPath        bool
	RedirectTrailingSlash  bool
	RedirectFixedPath      bool
	HandleMethodNotAllowed bool
	HandleOPTIONS          bool
}

type state struct {
	withDocs            bool
	corsEnabled         bool
	swagFound           bool
	generateSwaggerJson bool
	generateGoComments  bool
	docsPatterns        []*Route
}

// Router is a http.Handler which can be used to dispatch requests to different
// handler functions via configurable routes
type Router struct {
	Server          *http.Server
	AutoCertManager *autocert.Manager
	Config          *Config
	RouterConfig    *RouterConfig
	state           *state
	trees           map[string]*node
	middlewares     []func(http.Handler) http.Handler
	proxies         *kmap.SafeMap[string, http.Handler]
	paramsPool      sync.Pool
	secure          bool
	maxParams       uint16
	sig             chan os.Signal
}

type Config struct {
	Address     string
	Domain      string
	SubDomains  []string
	CertPath    string
	CertKeyPath string
	MediaDir    string
	isTls       bool
	host        string
	port        string
	onShutdown  []func() error
}

// New returns a new initialized Router.
// Path auto-correction, including trailing slashes, is enabled by default.
func New(config ...Config) *Router {
	router := &Router{
		RouterConfig: &RouterConfig{
			ReadTimeout:            5 * time.Second,
			WriteTimeout:           20 * time.Second,
			IdleTimeout:            20 * time.Second,
			RedirectTrailingSlash:  true,
			RedirectFixedPath:      true,
			HandleMethodNotAllowed: true,
			HandleOPTIONS:          true,
		},
		Config: &Config{
			Address:  "localhost:9313",
			MediaDir: "media",
			host:     "localhost",
			port:     "9313",
		},
		state: &state{
			generateGoComments: true,
			docsPatterns:       []*Route{},
		},
	}
	if len(config) > 0 {
		router.Config = &config[0]
		if router.Config.Address != "" {
			host, port, err := net.SplitHostPort(router.Config.Address)
			if err == nil {
				if host != "" {
					router.Config.host = host
				} else {
					router.Config.host = "localhost"
				}
				if port != "" {
					if port == "443" {
						router.secure = true
					}
					router.Config.port = port
				}
				router.Config.Address = router.Config.host + ":" + router.Config.port
			} else {
				lg.ErrorC("Address not valid")
				return router
			}
		}

		if router.Config.CertPath != "" {
			router.Config.isTls = true
			if router.Config.Domain != "" {
				router.Config.Address = router.Config.Domain
				router.Config.Domain = ""
			}
			host, port, err := net.SplitHostPort(router.Config.Address)
			if err == nil {
				if host != "" {
					router.Config.host = host
				}
				if port != "" {
					if port == "443" {
						router.secure = true
					}
					router.Config.port = port
				}
				if host == "" && port == "" {
					router.Config.port = "443"
					router.secure = true
				}
			} else {
				lg.ErrorC("Address not valid")
				return router
			}
		}

		if router.Config.Domain != "" {
			router.Config.isTls = true
			router.secure = true
			host, port, _ := net.SplitHostPort(router.Config.Address)
			if host == "" && port == "" {
				router.Config.port = "443"
			}
			if port != "" {
				router.Config.port = port
			} else {
				port = "443"
				err := checkDomain(router.Config.Domain)
				if err == nil {
					router.Config.Address = router.Config.Domain
				}
			}
		}

	}
	if router.Config.Address != "" {
		allrouters.Set(router.Config.Address, router)
	} else if router.Config.Domain != "" {
		allrouters.Set(router.Config.Domain, router)
	}
	router.sig = make(chan os.Signal, 1)
	return router
}

func GetRouters() *kmap.SafeMap[string, *Router] {
	return allrouters
}

func GetRouter(addrOrDomain string) (*Router, bool) {
	return allrouters.Get(addrOrDomain)
}

func GetFirstRouter() *Router {
	if firstRouter != nil {
		return firstRouter
	}
	allrouters.Range(func(_ string, value *Router) bool {
		firstRouter = value
		return false
	})
	return firstRouter
}

func (router *Router) IsTls() bool {
	return router.Config.isTls
}
func (router *Router) Address() string {
	return router.Config.Address
}
func (router *Router) Host() string {
	return router.Config.host
}
func (router *Router) Port() string {
	return router.Config.port
}

// Stop gracefully shuts down the server.
// It triggers the shutdown process by sending an interrupt signal and waits for completion.
func (router *Router) Stop() {
	if router.Server == nil {
		return
	}
	// Send interrupt signal to trigger shutdown
	router.sig <- os.Interrupt
}

func (router *Router) Signal() chan os.Signal {
	if router.Server == nil {
		return nil
	}
	return router.sig
}

// Restart shuts down the current server, runs cleanup callbacks, and starts a new server instance
func (router *Router) Restart(cleanupCallbacks ...func() error) error {
	// Run cleanup callbacks if any
	for _, cleanup := range cleanupCallbacks {
		if err := cleanup(); err != nil {
			return fmt.Errorf("cleanup callback error: %v", err)
		}
	}

	// Get current executable path
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}

	// Shutdown current server first
	router.Stop()

	// Wait a moment for the server to fully stop
	time.Sleep(1 * time.Second)

	// Start the new process using exec.Command
	cmd := exec.Command(executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start new process: %v", err)
	}

	// Exit current process
	os.Exit(0)
	return nil
}

// Run HTTP server on address
func (router *Router) Run() {
	router.initServer(router.Config.Address)

	// Create a done channel to handle shutdown
	serverDone := make(chan struct{})

	// Listen and serve
	go func() {
		if err := router.Server.ListenAndServe(); err != http.ErrServerClosed {
			lg.Error("Unable to shutdown the server", "err", err)
			os.Exit(1)
		} else {
			lg.Info("Server Off")
		}
		close(serverDone)
	}()

	if router.state.generateSwaggerJson {
		DocsGeneralDefaults.Host = router.Config.Address
		for i := len(router.state.docsPatterns) - 1; i >= 0; i-- {
			route := router.state.docsPatterns[i]
			if route.Docs == nil || !route.Docs.Triggered {
				router.state.docsPatterns = append(router.state.docsPatterns[:i], router.state.docsPatterns[i+1:]...)
			}
		}
		if router.state.generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}

	lg.Printfs("mgrunning on http://%s\n", router.Config.Address)

	// Wait for interrupt signal
	signal.Notify(router.sig, os.Interrupt)
	<-router.sig

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run any registered shutdown handlers
	for _, sh := range router.Config.onShutdown {
		if err := sh(); err != nil {
			lg.Error("shutdown handler error:", "err", err)
		}
	}

	// Attempt graceful shutdown
	if err := router.Server.Shutdown(ctx); err != nil {
		lg.Error("shutdown error:", "err", err)
	}

	// Wait for server to finish
	<-serverDone
}

// RunTLS HTTPS server using certificates
func (router *Router) RunTLS() {
	router.initServer(router.Config.Address)
	router.Server.TLSConfig.MinVersion = tls.VersionTLS12
	router.Server.TLSConfig.NextProtos = append([]string{"h2", "http/1.1"}, router.Server.TLSConfig.NextProtos...)

	// Create a done channel to handle shutdown
	serverDone := make(chan struct{})

	go func() {
		lg.Printfs("mgrunning on https://%s\n", router.Config.Address)
		if err := router.Server.ListenAndServeTLS(router.Config.CertPath, router.Config.CertKeyPath); err != http.ErrServerClosed {
			lg.Error("Unable to shutdown the server", "err", err)
			os.Exit(1)
		} else {
			lg.Info("Server Off")
		}
		close(serverDone)
	}()

	if router.state.generateSwaggerJson {
		DocsGeneralDefaults.Host = router.Config.Address
		for i := len(router.state.docsPatterns) - 1; i >= 0; i-- {
			route := router.state.docsPatterns[i]
			if route.Docs == nil || !route.Docs.Triggered {
				router.state.docsPatterns = append(router.state.docsPatterns[:i], router.state.docsPatterns[i+1:]...)
			}
		}
		if router.state.generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}

	// Wait for interrupt signal
	signal.Notify(router.sig, os.Interrupt)
	<-router.sig

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run any registered shutdown handlers
	for _, sh := range router.Config.onShutdown {
		if err := sh(); err != nil {
			lg.Error("shutdown handler error:", "err", err)
		}
	}

	// Attempt graceful shutdown
	if err := router.Server.Shutdown(ctx); err != nil {
		lg.Error("shutdown error:", "err", err)
	}

	// Wait for server to finish
	<-serverDone
}

// RunAutoTLS HTTPS server generate certificates and handle renew
func (router *Router) RunAutoTLS() {
	if router.proxies.Len() > 0 {
		if len(router.Config.SubDomains) != router.proxies.Len() {
			prs := router.proxies.Keys()
			for _, pr := range prs {
				found := false
				for _, d := range router.Config.SubDomains {
					if d == pr {
						found = true
						break
					}
				}
				if !found {
					router.Config.SubDomains = append(router.Config.SubDomains, pr)
				}
			}
		}
	}

	if router.AutoCertManager == nil {
		certManager, tlsconf := router.CreateServerCerts(router.Config.Domain, router.Config.SubDomains...)
		if certManager == nil || tlsconf == nil {
			lg.Fatal("unable to create tlsconfig")
			return
		}
		router.AutoCertManager = certManager
	}
	router.initAutoServer(router.AutoCertManager.TLSConfig())

	// Create a done channel to handle shutdown
	serverDone := make(chan struct{})

	// Start HTTP challenge server
	go http.ListenAndServe(":80", router.AutoCertManager.HTTPHandler(nil))

	go func() {
		lg.Printfs("mgrunning on https://%s , subdomains: %v\n", router.Config.Domain, router.Config.SubDomains)
		if err := router.Server.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			lg.Error("Unable to run the server", "err", err)
			os.Exit(1)
		} else {
			lg.Printfs("grServer Off !\n")
		}
		close(serverDone)
	}()

	if router.state.generateSwaggerJson {
		DocsGeneralDefaults.Host = router.Config.Domain
		for i := len(router.state.docsPatterns) - 1; i >= 0; i-- {
			route := router.state.docsPatterns[i]
			if route.Docs == nil || !route.Docs.Triggered {
				router.state.docsPatterns = append(router.state.docsPatterns[:i], router.state.docsPatterns[i+1:]...)
			}
		}
		if router.state.generateGoComments {
			GenerateGoDocsComments()
		}
		GenerateJsonDocs()
		OnDocsGenerationReady()
	}

	// Wait for interrupt signal
	signal.Notify(router.sig, os.Interrupt)
	<-router.sig

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run any registered shutdown handlers
	for _, sh := range router.Config.onShutdown {
		if err := sh(); err != nil {
			lg.Error("shutdown handler error:", "err", err)
		}
	}

	// Attempt graceful shutdown
	if err := router.Server.Shutdown(ctx); err != nil {
		lg.Error("shutdown error:", "err", err)
	}

	// Wait for server to finish
	<-serverDone
}

// Get is a shortcut for router.Handle(http.MethodGet, path, handler)
func (r *Router) Get(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodGet, path, handler, origines...)
}

func (gr *GroupRouter) Get(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodGet, gr.Group+pattern, handler, origines...)
}

// Head is a shortcut for router.Handle(http.MethodHead, path, handler)
func (r *Router) Head(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodHead, path, handler, origines...)
}

func (gr *GroupRouter) Head(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodHead, gr.Group+pattern, handler, origines...)
}

// Options is a shortcut for router.Handle(http.MethodOptions, path, handler)
func (r *Router) Options(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodOptions, path, handler, origines...)
}

func (gr *GroupRouter) Options(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodOptions, gr.Group+pattern, handler, origines...)
}

// Post is a shortcut for router.Handle(http.MethodPost, path, handler)
func (r *Router) Post(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodPost, path, handler, origines...)
}

func (gr *GroupRouter) Post(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodPost, gr.Group+pattern, handler, origines...)
}

// Put is a shortcut for router.Handle(http.MethodPut, path, handler)
func (r *Router) Put(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodPut, path, handler, origines...)
}

func (gr *GroupRouter) Put(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodPut, gr.Group+pattern, handler, origines...)
}

// Patch is a shortcut for router.Handle(http.MethodPatch, path, handler)
func (r *Router) Patch(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodPatch, path, handler, origines...)
}

func (gr *GroupRouter) Patch(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodPatch, gr.Group+pattern, handler, origines...)
}

// Delete is a shortcut for router.Handle(http.MethodDelete, path, handler)
func (r *Router) Delete(path string, handler Handler, origines ...string) *Route {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return r.Handle(http.MethodDelete, path, handler, origines...)
}

func (gr *GroupRouter) Delete(pattern string, handler Handler, origines ...string) *Route {
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
	return gr.Router.Handle(http.MethodDelete, gr.Group+pattern, handler, origines...)
}

// Handle registers a new request handler with the given path and method.
//
// For GET, POST, PUT, PATCH and DELETE requests the respective shortcut
// functions can be used.
//
// This function is intended for bulk loading and to allow the usage of less
// frequently used, non-standardized or custom methods (e.g. for internal
// communication with a proxy).
func (r *Router) Handle(method, path string, handler Handler, origines ...string) *Route {
	varsCount := uint16(0)

	if method == "" {
		lg.Error("method cannot be empty", "path", path)
		return nil
	}
	if handler == nil {
		lg.Error("missing handler", "path", path)
		return nil
	}

	if r.RouterConfig.SaveMatchedPath {
		varsCount++
		handler = r.saveMatchedRoutePath(path, handler)
	}
	route := Route{}
	route.Method = method
	route.Pattern = path
	route.Handler = handler
	if r.state.corsEnabled && len(origines) > 0 {
		route.Origines = strings.Join(origines, ",")
	}
	if r.state.withDocs && !strings.Contains(path, "*") && method != "WS" && method != "SSE" {
		d := &DocsRoute{
			Pattern:     path,
			Summary:     "A " + method + " request on " + path,
			Description: "A " + method + " request on " + path,
			Method:      strings.ToLower(method),
			Accept:      "json",
			Produce:     "json",
			Params:      []string{},
		}
		route.Docs = d
		r.state.docsPatterns = append(r.state.docsPatterns, &route)
	}

	if r.trees == nil {
		r.trees = make(map[string]*node)
	}

	root := r.trees[method]
	if root == nil {
		root = new(node)
		r.trees[method] = root

		r.RouterConfig.globalAllowed = r.allowed("*", "")
	}

	root.addPath(path, handler, origines...)

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
	return &route
}

// HandlerFunc is an adapter which allows the usage of an http.HandlerFunc as a
// request handler.
func (r *Router) HandlerFunc(method, path string, handler http.HandlerFunc, origines ...string) *Route {
	return r.Handle(method, path, func(c *Context) {
		if len(c.Params) > 0 {
			ctx := c.Request.Context()
			ctx = context.WithValue(ctx, ctxKey, c.Params)
			c.Request = c.Request.WithContext(ctx)
		}
		handler.ServeHTTP(c.ResponseWriter, c.Request)
	}, origines...)
}

func (handler Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := contextPool.Get().(*Context)
	ctx.ResponseWriter = w
	ctx.Request = r
	ctx.Params = GetParamsFromCtx(r.Context())
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
		lg.Error("path must end with /*filepath in path", "path", path)
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
		ty = strings.TrimPrefix(ty, "/")
		ty = strings.TrimSuffix(ty, "/")
		switch ty {
		case "pprof", "":
			pprof.Index(c.ResponseWriter, c.Request)
			return
		case "profile":
			pprof.Profile(c.ResponseWriter, c.Request)
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

// WithDocs check and install swagger, and generate json and go docs at the end , after the server run, you can use ksmux.OnDocsGenerationReady()
// genGoDocs default to true if genJsonDocs
func (router *Router) WithDocs(genJsonDocs bool, genGoDocs ...bool) *Router {
	router.state.withDocs = true
	router.state.generateSwaggerJson = genJsonDocs
	if len(genGoDocs) > 0 && !genGoDocs[0] {
		router.state.generateGoComments = false
	}
	if !router.state.swagFound && genJsonDocs {
		err := CheckAndInstallSwagger()
		if lg.CheckError(err) {
			return router
		}
	}
	return router
}

// EnableDomainCheck enable only the domain check from ksmux methods Get,Post,... (does not add cors middleware)
func (router *Router) EnableDomainCheck() {
	if !router.state.corsEnabled {
		router.state.corsEnabled = true
	}
}

// ServeHTTP makes the router implement the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.RouterConfig.PanicHandler != nil {
		defer r.recv(w, req)
	}

	if r.secure {
		if v := os.Getenv("SSL_MODE"); v != "" && v == "dev" {
			w.Header().Set("Strict-Transport-Security", "max-age=0; includeSubDomains")
		} else {
			w.Header().Set("Strict-Transport-Security", "max-age=15768000 ; includeSubDomains")
		}
	}

	path := req.URL.Path
	hasTrailingSlash := len(path) > 1 && path[len(path)-1] == '/'

	if root := r.trees[req.Method]; root != nil {
		if handler, ps, _, origines := root.getHandler(path, r.getParams); handler != nil {
			if r.state.corsEnabled && origines != "" && !strings.Contains(origines, "*") && !strings.Contains(origines, req.Header.Get("Origin")) {
				http.Error(w,
					http.StatusText(http.StatusForbidden),
					http.StatusForbidden,
				)
				return
			}
			c := contextPool.Get().(*Context)
			c.ResponseWriter = w
			c.Request = req
			defer contextPool.Put(c)
			if ps != nil {
				c.Params = *ps
				handler(c)
				r.putParams(ps)
				c.reset()
			} else {
				handler(c)
				c.reset()
			}
			return
		} else if r.RouterConfig.RedirectTrailingSlash && hasTrailingSlash {
			// Try without trailing slash
			pathWithoutSlash := path[:len(path)-1]
			if handler, _, _, _ := root.getHandler(pathWithoutSlash, nil); handler != nil {
				code := http.StatusMovedPermanently // 301 for GET requests
				if req.Method != http.MethodGet {
					code = http.StatusPermanentRedirect // 308 for all other methods
				}
				req.URL.Path = pathWithoutSlash
				http.Redirect(w, req, req.URL.String(), code)
				return
			}
		}
	}

	// Handle OPTIONS
	if req.Method == http.MethodOptions && r.RouterConfig.HandleOPTIONS {
		if allow := r.allowed(path, http.MethodOptions); allow != "" {
			w.Header().Set("Allow", allow)
			if r.RouterConfig.GlobalOPTIONS != nil {
				r.RouterConfig.GlobalOPTIONS.ServeHTTP(w, req)
			}
			return
		}
	} else if r.RouterConfig.HandleMethodNotAllowed {
		if allow := r.allowed(path, req.Method); allow != "" {
			w.Header().Set("Allow", allow)
			if r.RouterConfig.MethodNotAllowed != nil {
				r.RouterConfig.MethodNotAllowed.ServeHTTP(w, req)
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
	if r.RouterConfig.NotFound != nil {
		r.RouterConfig.NotFound.ServeHTTP(w, req)
	} else {
		http.NotFound(w, req)
	}
}

func (router *Router) initServer(addr string) {
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
		ReadTimeout:  router.RouterConfig.ReadTimeout,
		WriteTimeout: router.RouterConfig.WriteTimeout,
		IdleTimeout:  router.RouterConfig.IdleTimeout,
	}
	router.Server = &server
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
		Addr:         ":" + router.Config.port,
		Handler:      h,
		ReadTimeout:  router.RouterConfig.ReadTimeout,
		WriteTimeout: router.RouterConfig.WriteTimeout,
		IdleTimeout:  router.RouterConfig.IdleTimeout,
		TLSConfig:    tlsconf,
	}
	router.Server = &server
}

func (r *Router) PrintTree() {
	fmt.Println("Router Tree:")
	for method, root := range r.trees {
		fmt.Printf("\n[%s]\n", method)
		printNode(root, "", true)
	}
}

func printNode(n *node, prefix string, isLast bool) {
	// Choose the symbols for the tree structure
	nodePrefix := "└── "
	childPrefix := "    "
	if !isLast {
		nodePrefix = "├── "
		childPrefix = "│   "
	}

	// Print current node
	handlerStr := ""
	if n.handler != nil {
		handlerStr = " [handler]"
	}
	paramStr := ""
	if n.paramInfo != nil && len(n.paramInfo.names) > 0 {
		paramStr = " " + fmt.Sprint(n.paramInfo.names)
	}

	if n.nType == catchAll {
		// Remove the * from path for catchAll
		cleanPath := strings.TrimPrefix(n.path, "*")
		fmt.Printf("%s%s*%s%s%s\n", prefix, nodePrefix, cleanPath, paramStr, handlerStr)
	} else if n.nType == param {
		// Remove the : from path for params
		cleanPath := strings.TrimPrefix(n.path, ":")
		fmt.Printf("%s%s:%s%s%s\n", prefix, nodePrefix, cleanPath, paramStr, handlerStr)
	} else {
		fmt.Printf("%s%s%s%s%s\n", prefix, nodePrefix, n.path, paramStr, handlerStr)
	}

	// Print children
	for i, child := range n.children {
		isLastChild := i == len(n.children)-1
		printNode(child, prefix+childPrefix, isLastChild)
	}
}

// ToMap returns a map of all registered paths and their node
func (r *Router) ToMap() map[string]*node {
	routes := make(map[string]*node)
	for method, root := range r.trees {
		nodeToMap(root, "", method, routes)
	}
	return routes
}

func nodeToMap(n *node, path string, method string, routes map[string]*node) {
	currentPath := path

	if n.nType == catchAll {
		currentPath = path + "/*" + strings.TrimPrefix(n.path, "*")
	} else if n.nType == param {
		// Use the first param name if available
		paramName := n.path
		if n.paramInfo != nil && len(n.paramInfo.names) > 0 {
			paramName = n.paramInfo.names[0]
		}
		currentPath = path + "/:" + strings.TrimPrefix(paramName, ":")
	} else if n.path != "" {
		if currentPath == "" {
			currentPath = "/" + strings.TrimPrefix(n.path, "/")
		} else {
			currentPath = currentPath + "/" + strings.TrimPrefix(n.path, "/")
		}
	}

	if n.handler != nil {
		routes[method+" "+currentPath] = n
	}

	for _, child := range n.children {
		nodeToMap(child, currentPath, method, routes)
	}
}
