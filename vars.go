// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"html/template"
	"net/http"
	"time"

	"github.com/kamalshkeir/kmap"
)

var (
	MEDIA_DIR                = "media"
	HOST                     = ""
	PORT                     = ""
	ADDRESS                  = ""
	DOMAIN                   = ""
	IsTLS                    = false
	SUBDOMAINS               = []string{}
	ReadTimeout              = 5 * time.Second
	WriteTimeout             = 20 * time.Second
	IdleTimeout              = 20 * time.Second
	FuncBeforeServerShutdown = func(srv *http.Server) error {
		return nil
	}
	// context
	MultipartSize          = 10 << 20
	beforeRenderHtml       = kmap.New[string, func(c *Context, data *map[string]any)](false)
	rawTemplates           = kmap.New[string, *template.Template](false)
	beforeRenderHtmlSetted = false
	// docs
	DocsOutJson           = "."
	DocsEntryFile         = "ksmuxdocs/ksmuxdocs.go"
	OnDocsGenerationReady = func() {}
	withDocs              = false
	corsEnabled           = false
	swagFound             = false
	generateSwaggerJson   = false
	generateGoComments    = true
	docsPatterns          = []*Route{}
	// ctx cookies
	COOKIES_Expires  = 24 * 7 * time.Hour
	COOKIES_SameSite = http.SameSiteStrictMode
	COOKIES_HttpOnly = true
	COOKIES_SECURE   = true
	// proxy
	proxyUsed bool
	proxies   = kmap.New[string, http.Handler](false)
)
