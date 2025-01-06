package ksmux

import (
	"html/template"
	"net/http"
	"time"

	"github.com/kamalshkeir/kmap"
)

var (
	firstRouter *Router
	// context templates
	beforeRenderHtml = kmap.New[string, func(c *Context, data *map[string]any)]()
	rawTemplates     = kmap.New[string, *template.Template]()
	// docs
	DocsOutJson           = "."
	DocsEntryFile         = "ksmuxdocs/ksmuxdocs.go"
	OnDocsGenerationReady = func() {}
	// ctx cookies
	COOKIES_Expires  = 24 * 7 * time.Hour
	COOKIES_SameSite = http.SameSiteStrictMode
	COOKIES_HttpOnly = true
	COOKIES_SECURE   = true
)
