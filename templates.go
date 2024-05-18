// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/kamalshkeir/kencoding/json"
	"github.com/kamalshkeir/lg"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var allTemplates = template.New("all_templates")

// BeforeRenderHtml executed before every html render, you can use reqCtx.Value(key).(type.User) for example and add data to templates globaly
func BeforeRenderHtml(uniqueName string, fn func(c *Context, data *map[string]any)) {
	beforeRenderHtml.Set(uniqueName, fn)
	beforeRenderHtmlSetted = true
}

func (router *Router) LocalStatics(dirPath, webPath string, handlerMiddlewares ...func(handler Handler) Handler) error {
	dirPath = filepath.ToSlash(dirPath)
	if _, err := os.Stat(dirPath); err != nil {
		return err
	}
	if webPath[0] != '/' {
		webPath = "/" + webPath
	}
	webPath = strings.TrimSuffix(webPath, "/")
	handler := func(c *Context) {
		http.StripPrefix(webPath, http.FileServer(http.Dir(dirPath))).ServeHTTP(c.ResponseWriter, c.Request)
	}
	for _, mid := range handlerMiddlewares {
		handler = mid(handler)
	}
	router.Get(webPath+"/*path", handler)
	return nil
}

func (router *Router) EmbededStatics(embeded embed.FS, pathLocalDir, webPath string, handlerMiddlewares ...func(handler Handler) Handler) error {
	pathLocalDir = filepath.ToSlash(pathLocalDir)
	if _, err := os.Stat(pathLocalDir); err != nil {
		return err
	}
	if webPath[0] != '/' {
		webPath = "/" + webPath
	}
	webPath = strings.TrimSuffix(webPath, "/")
	toembed_dir, err := fs.Sub(embeded, pathLocalDir)
	if err != nil {
		lg.Error("error serving embeded dir", "err", err)
		return err
	}
	toembed_root := http.FileServer(http.FS(toembed_dir))
	handler := func(c *Context) {
		http.StripPrefix(webPath, toembed_root).ServeHTTP(c.ResponseWriter, c.Request)
	}
	for _, mid := range handlerMiddlewares {
		handler = mid(handler)
	}
	router.Get(webPath+"/*path", handler)
	return nil
}

func (router *Router) LocalTemplates(pathToDir string) error {
	cleanRoot := filepath.ToSlash(pathToDir)
	pfx := len(cleanRoot) + 1

	err := filepath.Walk(cleanRoot, func(path string, info os.FileInfo, e1 error) error {
		if !info.IsDir() && strings.HasSuffix(path, ".html") {
			if e1 != nil {
				return e1
			}

			b, e2 := os.ReadFile(path)
			if e2 != nil {
				return e2
			}
			name := filepath.ToSlash(path[pfx:])
			t := allTemplates.New(name).Funcs(functions)
			_, e2 = t.Parse(string(b))
			if e2 != nil {
				return e2
			}
		}

		return nil
	})

	return err
}

func (router *Router) EmbededTemplates(template_embed embed.FS, rootDir string) error {
	cleanRoot := filepath.ToSlash(rootDir)
	pfx := len(cleanRoot) + 1

	err := fs.WalkDir(template_embed, cleanRoot, func(path string, info fs.DirEntry, e1 error) error {
		if lg.CheckError(e1) {
			return e1
		}
		if !info.IsDir() && strings.HasSuffix(path, ".html") {
			b, e2 := template_embed.ReadFile(path)
			if lg.CheckError(e2) {
				return e2
			}

			name := filepath.ToSlash(path[pfx:])
			t := allTemplates.New(name).Funcs(functions)
			_, e3 := t.Parse(string(b))
			if lg.CheckError(e3) {
				return e2
			}
		}

		return nil
	})

	return err
}

func (router *Router) ServeLocalFile(file, endpoint, contentType string) {
	router.Get(endpoint, func(c *Context) {
		c.ServeFile(contentType, file)
	})
}

func (router *Router) ServeEmbededFile(file []byte, endpoint, contentType string) {
	router.Get(endpoint, func(c *Context) {
		c.ServeEmbededFile(contentType, file)
	})
}

func NewFuncMap(funcMap map[string]any) {
	for k, v := range funcMap {
		if _, ok := functions[k]; ok {
			lg.Error("unable to add, already exist", "func", k)
		} else {
			functions[k] = v
		}
	}
}

func (router *Router) NewFuncMap(funcMap map[string]any) {
	for k, v := range funcMap {
		if _, ok := functions[k]; ok {
			lg.Error("unable to add, already exist", "func", k)
		} else {
			functions[k] = v
		}
	}
}

func (router *Router) NewTemplateFunc(funcName string, function any) {
	if _, ok := functions[funcName]; ok {
		lg.Error("unable to add, already exist", "func", funcName)
	} else {
		functions[funcName] = function
	}
}

/* FUNC MAPS */
var functions = template.FuncMap{
	"contains": func(str string, substrings ...string) bool {
		for _, substr := range substrings {
			if strings.Contains(strings.ToLower(str), substr) {
				return true
			}
		}
		return false
	},
	"mapKV": func(kvs ...any) map[string]any {
		res := map[string]any{}
		if len(kvs)%2 != 0 {
			lg.Error(fmt.Sprintf("kvs in mapKV template func is not even: %v with length %d", kvs, len(kvs)))
			return res
		}
		for i := 0; i < len(kvs); i += 2 {
			key, ok := kvs[i].(string)
			if !ok {
				lg.Error(fmt.Sprintf("argument at position %d must be a string (key)", i))
				res["error"] = fmt.Errorf("argument at position %d must be a string (key)", i).Error()
				return res
			}

			res[key] = kvs[i+1]
		}
		return res
	},
	"startWith": func(str string, substrings ...string) bool {
		for _, substr := range substrings {
			if strings.HasPrefix(strings.ToLower(str), substr) {
				return true
			}
		}
		return false
	},
	"finishWith": func(str string, substrings ...string) bool {
		for _, substr := range substrings {
			if strings.HasSuffix(strings.ToLower(str), substr) {
				return true
			}
		}
		return false
	},
	"jsonIndented": func(data any) string {
		d, err := json.MarshalIndent(data, "", "\t")
		if err != nil {
			d = []byte("cannot marshal data")
		}
		return string(d)
	},
	"generateUUID": func() template.HTML {
		uuid, _ := GenerateUUID()
		return template.HTML(uuid)
	},
	"add": func(a int, b int) int {
		return a + b
	},
	"safe": func(str string) template.HTML {
		return template.HTML(str)
	},
	"jsTime": func(t any) string {
		valueToReturn := ""
		switch v := t.(type) {
		case time.Time:
			if !v.IsZero() {
				valueToReturn = v.Format("2006-01-02T15:04")
			} else {
				valueToReturn = time.Now().Format("2006-01-02T15:04")
			}
		case int:
			valueToReturn = time.Unix(int64(v), 0).Format("2006-01-02T15:04")
		case uint:
			valueToReturn = time.Unix(int64(v), 0).Format("2006-01-02T15:04")
		case int64:
			valueToReturn = time.Unix(v, 0).Format("2006-01-02T15:04")
		case string:
			if len(v) >= len("2006-01-02T15:04") && strings.Contains(v[:13], "T") {
				p, err := time.Parse("2006-01-02T15:04", v)
				if lg.CheckError(err) {
					valueToReturn = time.Now().Format("2006-01-02T15:04")
				} else {
					valueToReturn = p.Format("2006-01-02T15:04")
				}
			} else {
				if len(v) >= 16 {
					p, err := time.Parse("2006-01-02 15:04", v[:16])
					if lg.CheckError(err) {
						valueToReturn = time.Now().Format("2006-01-02T15:04")
					} else {
						valueToReturn = p.Format("2006-01-02T15:04")
					}
				}
			}
		default:
			if v != nil {
				lg.Error(fmt.Sprintf("type of %v %T is not handled,type is: %v", t, v, v))
			}
			valueToReturn = ""
		}
		return valueToReturn
	},
	"date": func(t any) string {
		dString := "02 Jan 2006"
		valueToReturn := ""
		switch v := t.(type) {
		case time.Time:
			if !v.IsZero() {
				valueToReturn = v.Format(dString)
			} else {
				valueToReturn = time.Now().Format(dString)
			}
		case string:
			if len(v) >= len(dString) && strings.Contains(v[:13], "T") {
				p, err := time.Parse(dString, v)
				if lg.CheckError(err) {
					valueToReturn = time.Now().Format(dString)
				} else {
					valueToReturn = p.Format(dString)
				}
			} else {
				if len(v) >= 16 {
					p, err := time.Parse(dString, v[:16])
					if lg.CheckError(err) {
						valueToReturn = time.Now().Format(dString)
					} else {
						valueToReturn = p.Format(dString)
					}
				}
			}
		default:
			if v != nil {
				lg.Error(fmt.Sprintf("type of %v is not handled,type is: %v", t, v))
			}
			valueToReturn = ""
		}
		return valueToReturn
	},
	"slug": func(str string) string {
		if len(str) == 0 {
			return ""
		}
		res, err := ToSlug(str)
		if err != nil {
			return ""
		}
		return res
	},
	"truncate": func(str any, size int) any {
		switch v := str.(type) {
		case string:
			if len(v) > size {
				return v[:size] + "..."
			} else {
				return v
			}
		default:
			return v
		}
	},
	"csrf_token": func(r *http.Request) template.HTML {
		csrf, _ := r.Cookie("csrf_token")
		if csrf != nil {
			return template.HTML(fmt.Sprintf("   <input type=\"hidden\" id=\"csrf_token\" value=\"%s\">   ", csrf.Value))
		} else {
			return template.HTML("")
		}
	},
}

// UUID

func GenerateUUID() (string, error) {
	var uuid [16]byte
	_, err := io.ReadFull(rand.Reader, uuid[:])
	if err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // Version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // Variant is 10
	var buf [36]byte
	encodeHex(buf[:], uuid)
	return string(buf[:]), nil
}

func encodeHex(dst []byte, uuid [16]byte) {
	hex.Encode(dst, uuid[:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], uuid[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], uuid[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], uuid[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:], uuid[10:])
}

func ToSlug(s string) (string, error) {
	str := []byte(strings.ToLower(s))

	// convert all spaces to dash
	regE := regexp.MustCompile("[[:space:]]")
	str = regE.ReplaceAll(str, []byte("-"))

	// remove all blanks such as tab
	regE = regexp.MustCompile("[[:blank:]]")
	str = regE.ReplaceAll(str, []byte(""))

	// remove all punctuations with the exception of dash

	regE = regexp.MustCompile("[!/:-@[-`{-~]")
	str = regE.ReplaceAll(str, []byte(""))

	regE = regexp.MustCompile("/[^\x20-\x7F]/")
	str = regE.ReplaceAll(str, []byte(""))

	regE = regexp.MustCompile("`&(amp;)?#?[a-z0-9]+;`i")
	str = regE.ReplaceAll(str, []byte("-"))

	regE = regexp.MustCompile("`&([a-z])(acute|uml|circ|grave|ring|cedil|slash|tilde|caron|lig|quot|rsquo);`i")
	str = regE.ReplaceAll(str, []byte("\\1"))

	regE = regexp.MustCompile("`[^a-z0-9]`i")
	str = regE.ReplaceAll(str, []byte("-"))

	regE = regexp.MustCompile("`[-]+`")
	str = regE.ReplaceAll(str, []byte("-"))

	strReplaced := strings.Replace(string(str), "&", "", -1)
	strReplaced = strings.Replace(strReplaced, `"`, "", -1)
	strReplaced = strings.Replace(strReplaced, "&", "-", -1)
	strReplaced = strings.Replace(strReplaced, "--", "-", -1)

	if strings.HasPrefix(strReplaced, "-") || strings.HasPrefix(strReplaced, "--") {
		strReplaced = strings.TrimPrefix(strReplaced, "-")
		strReplaced = strings.TrimPrefix(strReplaced, "--")
	}

	if strings.HasSuffix(strReplaced, "-") || strings.HasSuffix(strReplaced, "--") {
		strReplaced = strings.TrimSuffix(strReplaced, "-")
		strReplaced = strings.TrimSuffix(strReplaced, "--")
	}

	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	slug, _, err := transform.String(t, strReplaced)

	if err != nil {
		return "", err
	}

	return strings.TrimSpace(slug), nil
}
