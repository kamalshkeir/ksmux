// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kamalshkeir/kencoding/json"
	"github.com/kamalshkeir/ksmux/ws"
	"github.com/kamalshkeir/lg"
)

var contextPool sync.Pool

type ContextKey string

func init() {
	contextPool.New = func() interface{} {
		return &Context{
			status: 200,
			Params: Params{},
		}
	}
}

type Context struct {
	http.ResponseWriter
	*http.Request
	Params Params
	status int
}

func (c *Context) Param(name string) string {
	return c.Params.ByName(name)
}

// Stream send SSE Streaming Response
func (c *Context) Stream(response string) error {
	defer c.Flush()
	_, err := c.ResponseWriter.Write([]byte("data: " + response + "\n\n"))
	if lg.CheckError(err) {
		return err
	}
	return nil
}

func (c *Context) UpgradeConnection() (*ws.Conn, error) {
	return ws.UpgradeConnection(c.ResponseWriter, c.Request, nil)
}

func (c *Context) SliceParams() Params {
	return c.Params
}

func (c *Context) reset() {
	c.Params = c.Params[:0]
	c.status = 200
	c.Request = nil
	c.ResponseWriter = nil
}

// Context return request context
func (c *Context) Context() context.Context {
	return c.Request.Context()
}

// Status set status to context, will not be writed to header
func (c *Context) Status(code int) *Context {
	c.status = code
	return c
}

// Text return text with custom code to the client
func (c *Context) Text(body string) {
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)
	_, err := c.ResponseWriter.Write([]byte(body))
	lg.CheckError(err)
}

// SetCookie set cookie given key and value
func (c *Context) SetCookie(key, value string, maxAge ...time.Duration) {
	if !COOKIES_SECURE {
		if c.Request.TLS != nil {
			COOKIES_SECURE = true
		}
	}
	// if corsEnabled {
	// 	COOKIES_SameSite = http.SameSiteNoneMode
	// }
	var ma int
	if len(maxAge) > 0 {
		ma = int(maxAge[0].Seconds())
		http.SetCookie(c.ResponseWriter, &http.Cookie{
			Name:     key,
			Value:    value,
			Path:     "/",
			Expires:  time.Now().Add(maxAge[0]),
			HttpOnly: COOKIES_HttpOnly,
			SameSite: COOKIES_SameSite,
			Secure:   COOKIES_SECURE,
			MaxAge:   ma,
		})
	} else {
		ma = int(COOKIES_Expires.Seconds())
		http.SetCookie(c.ResponseWriter, &http.Cookie{
			Name:     key,
			Value:    value,
			Path:     "/",
			Expires:  time.Now().Add(COOKIES_Expires),
			HttpOnly: COOKIES_HttpOnly,
			SameSite: COOKIES_SameSite,
			Secure:   COOKIES_SECURE,
			MaxAge:   ma,
		})
	}
}

// GetCookie get cookie with specific key
func (c *Context) GetCookie(key string) (string, error) {
	v, err := c.Request.Cookie(key)
	if err != nil {
		return "", err
	}
	return v.Value, nil
}

// DeleteCookie delete cookie with specific key
func (c *Context) DeleteCookie(key string) {
	http.SetCookie(c.ResponseWriter, &http.Cookie{
		Name:     key,
		Value:    "",
		Path:     "/",
		Expires:  time.Now(),
		HttpOnly: COOKIES_HttpOnly,
		SameSite: COOKIES_SameSite,
		Secure:   COOKIES_SECURE,
		MaxAge:   -1,
	})
}

func (c *Context) MatchedPath() string {
	return c.Params.ByName(MatchedRoutePathParam)
}

// AddHeader Add append a header value to key if exist
func (c *Context) AddHeader(key, value string) {
	c.ResponseWriter.Header().Add(key, value)
}

// SetHeader Set the header value to the new value, old removed
func (c *Context) SetHeader(key, value string) {
	c.ResponseWriter.Header().Set(key, value)
}

// SetHeader Set the header value to the new value, old removed
func (c *Context) SetStatus(statusCode int) {
	c.status = statusCode
	c.WriteHeader(statusCode)
}

// QueryParam get query param
func (c *Context) QueryParam(name string) string {
	return c.Request.URL.Query().Get(name)
}

// Json return json to the client
func (c *Context) Json(data any) {
	c.SetHeader("Content-Type", "application/json")
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)
	by, err := json.Marshal(data)
	if !lg.CheckError(err) {
		_, err = c.ResponseWriter.Write(by)
		lg.CheckError(err)
	}
}

// JsonIndent return json indented to the client
func (c *Context) JsonIndent(data any) {
	c.SetHeader("Content-Type", "application/json")
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)
	by, err := json.MarshalIndent(data, "", " \t")
	if !lg.CheckError(err) {
		_, err = c.ResponseWriter.Write(by)
		lg.CheckError(err)
	}
}

// Html return template_name with data to the client
func (c *Context) Html(template_name string, data map[string]any) {
	var buff bytes.Buffer
	if data == nil {
		data = make(map[string]any)
	}
	data["Request"] = c.Request
	if beforeRenderHtmlSetted {
		beforeRenderHtml.Range(func(key string, value func(reqCtx context.Context, data *map[string]any)) bool {
			value(c.Request.Context(), &data)
			return true
		})
	}

	err := allTemplates.ExecuteTemplate(&buff, template_name, data)
	if lg.CheckError(err) {
		c.status = http.StatusInternalServerError
		lg.Error("could not render", "err", err, "temp", template_name)
		http.Error(c.ResponseWriter, fmt.Sprintf("could not render %s : %v", template_name, err), c.status)
		return
	}

	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)

	_, err = buff.WriteTo(c.ResponseWriter)
	if lg.CheckError(err) {
		return
	}
}

// SaveRawHtml save templateRaw as templateName to be able to use it like c.RawHtml
func SaveRawHtml(templateRaw string, templateName string) {
	t, err := template.New("raw").Funcs(functions).Parse(templateRaw)
	if !lg.CheckError(err) {
		rawTemplates.Set(templateName, t)
	}
}

func ExecuteRawHtml(rawTemplateName string, data map[string]any) (string, error) {
	var buff bytes.Buffer
	if data == nil {
		data = make(map[string]any)
	}
	t, ok := rawTemplates.Get(rawTemplateName)
	if !ok {
		return "", fmt.Errorf("template not registered. Use ksmux.SaveRawHtml before using c.RawHtml")
	}
	if err := t.Execute(&buff, data); lg.CheckError(err) {
		return "", err
	}
	return buff.String(), nil
}

// NamedRawHtml render rawTemplateName with data using go engine, make sure to save the html using ksmux.SaveRawHtml outside the handler
func (c *Context) NamedRawHtml(rawTemplateName string, data map[string]any) error {
	var buff bytes.Buffer
	if data == nil {
		data = make(map[string]any)
	}
	data["Request"] = c.Request
	if beforeRenderHtmlSetted {
		beforeRenderHtml.Range(func(key string, value func(reqCtx context.Context, data *map[string]any)) bool {
			value(c.Request.Context(), &data)
			return true
		})
	}
	t, ok := rawTemplates.Get(rawTemplateName)
	if !ok {
		return fmt.Errorf("template not registered. Use ksmux.SaveRawHtml before using c.RawHtml")
	}

	if err := t.Execute(&buff, data); lg.CheckError(err) {
		return err
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)
	_, err := buff.WriteTo(c.ResponseWriter)
	if lg.CheckError(err) {
		return err
	}
	return nil
}

// NamedRawHtml render rawTemplateName with data using go engine, make sure to save the html using ksmux.SaveRawHtml outside the handler
func (c *Context) RawHtml(rawTemplate string, data map[string]any) error {
	var buff bytes.Buffer
	if data == nil {
		data = make(map[string]any)
	}
	data["Request"] = c.Request
	if beforeRenderHtmlSetted {
		beforeRenderHtml.Range(func(key string, value func(reqCtx context.Context, data *map[string]any)) bool {
			value(c.Request.Context(), &data)
			return true
		})
	}
	t, err := template.New("rawww").Funcs(functions).Parse(rawTemplate)
	if err != nil {
		return err
	}

	if err := t.Execute(&buff, data); lg.CheckError(err) {
		return err
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	if c.status == 0 {
		c.status = 200
	}
	c.WriteHeader(c.status)
	_, err = buff.WriteTo(c.ResponseWriter)
	if lg.CheckError(err) {
		return err
	}
	return nil
}

func (c *Context) IsAuthenticated(key ...string) bool {
	var k string
	if len(key) > 0 {
		k = key[0]
	} else {
		k = "user"
	}
	if user, _ := c.GetKey(k); user != nil {
		return true
	} else {
		return false
	}
}

// User is alias of c.Keys but have key default to 'user'
func (c *Context) User(key ...string) (any, bool) {
	var k string
	if len(key) > 0 {
		k = key[0]
	} else {
		k = "user"
	}
	return c.GetKey(k)
}

// GetKey return request context value for given key
func (c *Context) GetKey(key string) (any, bool) {
	v := c.Request.Context().Value(ContextKey(key))
	if v != nil {
		return v, true
	} else {
		return nil, false
	}
}

func (c *Context) SetKey(key string, value any) {
	ctx := context.WithValue(c.Request.Context(), ContextKey(key), value)
	c.Request = c.Request.WithContext(ctx)
}

func (c *Context) Flush() bool {
	f, ok := c.ResponseWriter.(http.Flusher)
	if ok {
		f.Flush()
	}
	return ok
}

// BodyJson get json body from request and return map
// USAGE : data := c.BodyJson(r)
func (c *Context) BodyJson() map[string]any {
	defer c.Request.Body.Close()
	d := map[string]any{}
	dec := json.NewDecoder(c.Request.Body)
	if err := dec.Decode(&d); err == io.EOF {
		//empty body
		lg.Error("empty body EOF")
		return nil
	} else if err != nil {
		lg.Error("error BodyJson: %v", err)
		return nil
	} else {
		return d
	}
}

// scan body to struct, default json
func (c *Context) BindBody(strctPointer any, isXML ...bool) error {
	defer c.Request.Body.Close()
	if len(isXML) > 0 && isXML[0] {
		dec := xml.NewDecoder(c.Request.Body)
		if err := dec.Decode(strctPointer); lg.CheckError(err) {
			return err
		}
	} else {
		dec := json.NewDecoder(c.Request.Body)
		if err := dec.Decode(strctPointer); lg.CheckError(err) {
			return err
		}
	}
	return nil
}

func (c *Context) BodyText() string {
	defer c.Request.Body.Close()
	b, err := io.ReadAll(c.Request.Body)
	if lg.CheckError(err) {
		return ""
	}
	return string(b)
}

func (c *Context) AddSSEHeaders() {
	controller := http.NewResponseController(c.ResponseWriter)
	controller.SetReadDeadline(time.Time{})
	controller.SetWriteDeadline(time.Time{})
	c.ResponseWriter.Header().Add("Content-Type", "text/event-stream")
	c.ResponseWriter.Header().Add("Cache-Control", "no-cache")
	c.ResponseWriter.Header().Add("Connection", "keep-alive")
}

// Redirect redirect the client to the specified path with a custom code, default status 307
func (c *Context) Redirect(path string) {
	if c.status == 0 {
		c.status = http.StatusTemporaryRedirect
	}
	http.Redirect(c.ResponseWriter, c.Request, path, c.status)
}

// ServeFile serve a file from handler
func (c *Context) ServeFile(content_type, path_to_file string) {
	c.SetHeader("Content-Type", content_type)
	http.ServeFile(c.ResponseWriter, c.Request, path_to_file)
}

// ServeEmbededFile serve an embeded file from handler
func (c *Context) ServeEmbededFile(content_type string, embed_file []byte) {
	c.SetHeader("Content-Type", content_type)
	_, err := c.ResponseWriter.Write(embed_file)
	lg.CheckError(err)
}

func (c *Context) ParseMultipartForm(size ...int64) (formData url.Values, formFiles map[string][]*multipart.FileHeader) {
	s := int64(32 << 20)
	if len(size) > 0 {
		s = size[0]
	}
	r := c.Request
	parseErr := r.ParseMultipartForm(s)
	if parseErr != nil {
		lg.Error("ParseMultipartForm error", "err", parseErr)
	}
	defer func() {
		err := r.MultipartForm.RemoveAll()
		lg.CheckError(err)
	}()
	formData = r.Form
	formFiles = r.MultipartForm.File
	return formData, formFiles
}

// SaveFile save file to path
func (c *Context) SaveFile(fileheader *multipart.FileHeader, path string) error {
	return SaveMultipartFile(fileheader, path)
}

// Error send json error
func (c *Context) Error(code int, message string) {
	c.Status(code).Json(map[string]any{
		"error":  message,
		"status": code,
	})
}

// SaveMultipartFile Save MultipartFile
func SaveMultipartFile(fh *multipart.FileHeader, path string) (err error) {
	var (
		f  multipart.File
		ff *os.File
	)
	f, err = fh.Open()
	if err != nil {
		return
	}

	var ok bool
	if ff, ok = f.(*os.File); ok {
		if err = f.Close(); err != nil {
			return
		}
		if os.Rename(ff.Name(), path) == nil {
			return nil
		}

		// Reopen f for the code below.
		if f, err = fh.Open(); err != nil {
			return
		}
	}

	defer func() {
		e := f.Close()
		if err == nil {
			err = e
		}
	}()

	if ff, err = os.Create(path); err != nil {
		return
	}
	defer func() {
		e := ff.Close()
		if err == nil {
			err = e
		}
	}()
	_, err = copyZeroAlloc(ff, f)
	return
}

// UploadFile upload received_filename into folder_out and return url,fileByte,error
func (c *Context) UploadFile(received_filename, folder_out string, acceptedFormats ...string) (string, []byte, error) {
	_, formFiles := c.ParseMultipartForm()

	url := ""
	data := []byte{}
	for inputName, files := range formFiles {
		var buff bytes.Buffer
		if received_filename == inputName {
			f := files[0]
			file, err := f.Open()
			if lg.CheckError(err) {
				return "", nil, err
			}
			defer file.Close()
			// copy the uploaded file to the buffer
			if _, err := io.Copy(&buff, file); err != nil {
				return "", nil, err
			}

			data_string := buff.String()

			// make DIRS if not exist
			err = os.MkdirAll(MEDIA_DIR+"/"+folder_out+"/", 0770)
			if err != nil {
				return "", nil, err
			}
			if len(acceptedFormats) == 0 || StringContains(f.Filename, acceptedFormats...) {
				dst, err := os.Create(MEDIA_DIR + "/" + folder_out + "/" + f.Filename)
				if err != nil {
					return "", nil, err
				}
				defer dst.Close()
				dst.Write([]byte(data_string))

				url = MEDIA_DIR + "/" + folder_out + "/" + f.Filename
				data = []byte(data_string)
			} else {
				lg.Error("not handled", "fname", f.Filename)
				return "", nil, fmt.Errorf("expecting filename to finish to be %v", acceptedFormats)
			}
		}

	}
	return url, data, nil
}

func (c *Context) UploadFiles(received_filenames []string, folder_out string, acceptedFormats ...string) ([]string, [][]byte, error) {
	_, formFiles := c.ParseMultipartForm()
	urls := []string{}
	datas := [][]byte{}
	for inputName, files := range formFiles {
		var buff bytes.Buffer
		if len(files) > 0 && SliceContains(received_filenames, inputName) {
			for _, f := range files {
				file, err := f.Open()
				if lg.CheckError(err) {
					return nil, nil, err
				}
				defer file.Close()
				// copy the uploaded file to the buffer
				if _, err := io.Copy(&buff, file); err != nil {
					return nil, nil, err
				}

				data_string := buff.String()

				// make DIRS if not exist
				err = os.MkdirAll(MEDIA_DIR+"/"+folder_out+"/", 0770)
				if err != nil {
					return nil, nil, err
				}
				if len(acceptedFormats) == 0 || StringContains(f.Filename, acceptedFormats...) {
					dst, err := os.Create(MEDIA_DIR + "/" + folder_out + "/" + f.Filename)
					if err != nil {
						return nil, nil, err
					}
					defer dst.Close()
					dst.Write([]byte(data_string))

					url := MEDIA_DIR + "/" + folder_out + "/" + f.Filename
					urls = append(urls, url)
					datas = append(datas, []byte(data_string))
				} else {
					lg.Error("not handled")
					return nil, nil, fmt.Errorf("file type not supported, accepted extensions: %v", acceptedFormats)
				}
			}
		}

	}
	return urls, datas, nil
}

// DELETE FILE
func (c *Context) DeleteFile(path string) error {
	err := os.Remove("." + path)
	if err != nil {
		return err
	} else {
		return nil
	}
}

// Download download data_bytes(content) asFilename(test.json,data.csv,...) to the client
func (c *Context) Download(data_bytes []byte, asFilename string) {
	bytesReader := bytes.NewReader(data_bytes)
	c.SetHeader("Content-Disposition", "attachment; filename="+strconv.Quote(asFilename))
	c.SetHeader("Content-Type", c.Request.Header.Get("Content-Type"))
	io.Copy(c.ResponseWriter, bytesReader)
}

func (c *Context) GetUserIP() string {
	IPAddress := c.Request.Header.Get("X-Real-Ip")
	if IPAddress == "" {
		IPAddress = c.Request.Header.Get("X-Forwarded-For")
	}
	if IPAddress == "" {
		IPAddress = c.Request.RemoteAddr
	}
	return IPAddress
}
