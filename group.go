package ksmux

import (
	"net/http"
	"strings"
)

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
	return gr.Router.Handle(http.MethodGet, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodHead, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodOptions, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodPost, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodPut, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodPatch, gr.Group+pattern, h, origines...)
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
	return gr.Router.Handle(http.MethodDelete, gr.Group+pattern, h, origines...)
}
