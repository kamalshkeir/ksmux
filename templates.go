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

	"github.com/kamalshkeir/kmap"
	"github.com/kamalshkeir/ksmux/jsonencdec"
	"github.com/kamalshkeir/lg"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	cachedTemplates = kmap.New[string, *template.Template]()
	tempFunc        func() error
)

// BeforeRenderHtml executed before every html render, you can use reqCtx.Value(key).(type.User) for example and add data to templates globaly
func BeforeRenderHtml(uniqueName string, fn func(c *Context, data *map[string]any)) {
	beforeRenderHtml.Set(uniqueName, fn)
}

func (router *Router) LocalStatics(dirPath, webPath string, handlerMiddlewares ...func(handler Handler) Handler) error {
	dirPath = strings.TrimSuffix(dirPath, "/")
	dirPath = filepath.ToSlash(dirPath)
	if _, err := os.Stat(dirPath); err != nil {
		return err
	}
	if webPath[0] != '/' {
		webPath = "/" + webPath
	}
	webPath = strings.TrimSuffix(webPath, "/")
	handler := func(c *Context) {
		if strings.Contains(c.Request.URL.Path, "docache") {
			c.SetHeader("Cache-Control", "max-age=31536000")
		}
		http.StripPrefix(webPath, http.FileServer(http.Dir(dirPath))).ServeHTTP(c.ResponseWriter, c.Request)
	}
	for _, mid := range handlerMiddlewares {
		handler = mid(handler)
	}
	router.Get(webPath+"/*path", handler)
	return nil
}

func (router *Router) EmbededStatics(embeded embed.FS, pathLocalDir, webPath string, handlerMiddlewares ...func(handler Handler) Handler) error {
	pathLocalDir = strings.TrimSuffix(pathLocalDir, "/")
	pathLocalDir = filepath.ToSlash(pathLocalDir)
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
		if strings.Contains(c.Request.URL.Path, "docache") {
			c.SetHeader("Cache-Control", "max-age=31536000")
		}
		http.StripPrefix(webPath, toembed_root).ServeHTTP(c.ResponseWriter, c.Request)
	}
	for _, mid := range handlerMiddlewares {
		handler = mid(handler)
	}
	router.Get(webPath+"/*path", handler)
	return nil
}

func (router *Router) LocalTemplates(pathToDir string) error {
	tempFunc = func() error {
		pathToDir = strings.TrimSuffix(pathToDir, "/")
		cleanRoot := filepath.ToSlash(pathToDir)
		pfx := len(cleanRoot) + 1

		// First collect and parse all layout files together
		var layoutFiles []string
		err := filepath.Walk(pathToDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, "layout.html") {
				layoutFiles = append(layoutFiles, path)
			}
			return nil
		})
		if err != nil {
			return err
		}

		// Now parse each non-layout template with all layouts
		return filepath.Walk(pathToDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, "layout.html") {
				name := filepath.ToSlash(path[pfx:])

				// Create a new template set with the page name
				t := template.New(name).Funcs(functions)

				// Parse all layouts first
				for _, layoutFile := range layoutFiles {
					content, err := os.ReadFile(layoutFile)
					if err != nil {
						lg.Error("Failed to read layout file", "path", layoutFile, "err", err)
						return err
					}
					_, err = t.Parse(string(content))
					if err != nil {
						lg.Error("Failed to parse layout file", "path", layoutFile, "err", err)
						return err
					}
				}

				// Then parse the page template
				content, err := os.ReadFile(path)
				if err != nil {
					lg.Error("Failed to read page template", "path", path, "err", err)
					return err
				}
				_, err = t.Parse(string(content))
				if err != nil {
					lg.Error("Failed to parse page template", "path", path, "err", err)
					return err
				}

				cachedTemplates.Set(name, t)
			}
			return nil
		})
	}
	return tempFunc()
}

func (router *Router) EmbededTemplates(template_embed embed.FS, rootDir string) error {
	tempFunc = func() error {
		rootDir = strings.TrimSuffix(rootDir, "/")
		cleanRoot := filepath.ToSlash(rootDir)
		pfx := len(cleanRoot) + 1

		// First collect all layout files
		var layoutFiles []string
		err := fs.WalkDir(template_embed, rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, "layout.html") {
				layoutFiles = append(layoutFiles, path)
			}
			return nil
		})
		if err != nil {
			return err
		}

		// Now parse each non-layout template with all layouts
		return fs.WalkDir(template_embed, rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, "layout.html") {
				name := filepath.ToSlash(path[pfx:])

				// Create a new template set with the page name
				t := template.New(name).Funcs(functions)

				// Parse all layouts first
				for _, layoutFile := range layoutFiles {
					content, err := template_embed.ReadFile(layoutFile)
					if err != nil {
						lg.Error("Failed to read layout file", "path", layoutFile, "err", err)
						return err
					}
					_, err = t.Parse(string(content))
					if err != nil {
						lg.Error("Failed to parse layout file", "path", layoutFile, "err", err)
						return err
					}
				}

				// Then parse the page template
				content, err := template_embed.ReadFile(path)
				if err != nil {
					lg.Error("Failed to read page template", "path", path, "err", err)
					return err
				}
				_, err = t.Parse(string(content))
				if err != nil {
					lg.Error("Failed to parse page template", "path", path, "err", err)
					return err
				}

				cachedTemplates.Set(name, t)
			}
			return nil
		})
	}
	return tempFunc()
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
	if tempFunc != nil {
		lg.CheckError(tempFunc())
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
	if tempFunc != nil {
		lg.CheckError(tempFunc())
	}
}

func (router *Router) NewTemplateFunc(funcName string, function any) {
	if _, ok := functions[funcName]; ok {
		lg.Error("unable to add, already exist", "func", funcName)
	} else {
		functions[funcName] = function
	}
	if tempFunc != nil {
		lg.CheckError(tempFunc())
	}
}

type templateMap map[string]any

func (t templateMap) Get(key string) any {
	if v, ok := t[key]; ok {
		return v
	}
	return nil
}

func (t templateMap) Set(kvs ...any) bool {
	if len(kvs)%2 != 0 {
		lg.Error(fmt.Sprintf("kvs in mapKV template func is not even: %v with length %d", kvs, len(kvs)))
		return false
	}
	for i := 0; i < len(kvs); i += 2 {
		key, ok := kvs[i].(string)
		if !ok {
			lg.Error(fmt.Sprintf("argument at position %d must be a string (key)", i))
			return false
		}

		t[key] = kvs[i+1]
	}
	return true
}

func (t templateMap) Delete(k string) bool {
	delete(t, k)
	return true
}

func (t templateMap) Keys() []string {
	res := make([]string, 0, len(t))
	for k := range t {
		res = append(res, k)
	}
	return res
}
func (t templateMap) Values() []any {
	res := make([]any, 0, len(t))
	for _, v := range t {
		res = append(res, v)
	}
	return res
}

func (t templateMap) Exists(k string) bool {
	_, ok := t[k]
	return ok
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
	"newMap": func(kvs ...any) templateMap {
		res := templateMap{}
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
			res.Set(key, kvs[i+1])
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
	"json": func(data any) string {
		d, err := jsonencdec.DefaultMarshalIndent(data, "", "\t")
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
