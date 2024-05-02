// Copyright 2020 Kamal SHKEIR. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.

package ksmux

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kamalshkeir/lg"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

var AutoCertRegexHostPolicy = false
var errPolicyMismatch = errors.New("the host did not match the allowed hosts")

func (router *Router) CreateServerCerts(domainName string, subDomains ...string) (*autocert.Manager, *tls.Config) {
	uniqueDomains := []string{}
	domainsToCertify := map[string]bool{}
	// add domainName
	err := checkDomain(domainName)
	if err == nil {
		domainsToCertify[domainName] = true
	}
	// add subdomains
	for _, sub := range subDomains {
		if _, ok := domainsToCertify[sub]; !ok {
			domainsToCertify[sub] = true
		}
	}
	for k := range domainsToCertify {
		uniqueDomains = append(uniqueDomains, k)
	}
	if len(uniqueDomains) > 0 {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache("certs"),
			HostPolicy: autocert.HostWhitelist(uniqueDomains...),
			Email:      os.Getenv("SSL_EMAIL"),
		}
		if v := os.Getenv("SSL_MODE"); v != "" && v == "dev" {
			m.Client = &acme.Client{
				DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
			}
		}

		tlsConfig := m.TLSConfig()
		tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)
		tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// Attempt to retrieve the certificate from the cache
			certData, err := m.Cache.Get(hello.Context(), hello.ServerName)
			if err == nil {
				// Certificate exists, parse it into a *tls.Certificate
				cert, err := tls.X509KeyPair(certData, nil)
				if err == nil {
					return &cert, nil
				}
			}

			// Certificate does not exist, request a new one
			cert, err := m.GetCertificate(hello)
			if lg.CheckError(err) {
				return nil, err
			}
			saveCertificateAndKey(cert)
			return cert, nil
		}
		if AutoCertRegexHostPolicy {
			sp := strings.Split(domainName, ".")
			if len(sp) > 2 {
				domainName = sp[1] + "." + sp[2]
			}
			domainNameReg := strings.ReplaceAll(domainName, ".", `\.`)
			allowedHosts := regexp.MustCompile(`^([a-zA-Z0-9]+(-[a-zA-Z0-9]+)*\.)?` + domainNameReg + `$`)
			m.HostPolicy = func(_ context.Context, host string) error {
				if allowedHosts.MatchString(host) {
					return nil
				}
				return errPolicyMismatch
			}
		}
		lg.Printfs("grAuto certified domains: %v\n", uniqueDomains)
		return m, tlsConfig
	}
	return nil, nil
}

func CopyFile(src, dst string, BUFFERSIZE int64) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	_, err = os.Stat(dst)
	if err == nil {
		return fmt.Errorf("file %s already exists", dst)
	}

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	buf := make([]byte, BUFFERSIZE)
	for {
		n, err := source.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}

		if _, err := destination.Write(buf[:n]); err != nil {
			return err
		}
	}
	return err
}

func SetSSLMode(ProdOrDev string) {
	switch ProdOrDev {
	case "dev", "Dev", "DEV":
		os.Setenv("SSL_MODE", "dev")
	default:
		os.Setenv("SSL_MODE", "prod")
	}
}

func SetSSLEmail(email string) {
	os.Setenv("SSL_EMAIL", email)
}

func saveCertificateAndKey(cert *tls.Certificate) {
	if cert.Leaf == nil {
		return
	}
	domain := cert.Leaf.Subject.CommonName

	// Determine the prefix based on the SSL_MODE environment variable
	var prefix string
	if v := os.Getenv("SSL_MODE"); v != "" && v == "dev" {
		prefix = "staging"
	} else {
		prefix = "prod"
	}

	if !isCertificateValid("certs/"+prefix+"_"+domain, 1) {
		// Certificate is older than 2 months, delete and return
		err := CopyFile("certs/"+domain, "certs/"+prefix+"_"+domain, 1024*1024)
		if lg.CheckError(err) {
			lg.ErrorC("Failed to copy main certificate", "err", err)
		}
	}

	// Save the certificate with the appropriate prefix and creation date
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	certFile := fmt.Sprintf("certs/%s_%s_cert.pem", prefix, domain)
	if !isCertificateValid(certFile, 1) {
		// Certificate is older than 1 months, delete and return
		_ = os.Remove(certFile)
	}

	err := os.WriteFile(certFile, certPEM, 0644)
	if lg.CheckError(err) {
		lg.ErrorC("Failed to save certificate", "err", err)
		return
	}

	// Save the private key with the same prefix and creation date
	var keyPEM []byte
	switch key := cert.PrivateKey.(type) {
	case *rsa.PrivateKey:
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(key)
		if lg.CheckError(err) {
			lg.Printfs("Unable to marshal ECDSA private key: %v\n", err)
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
	default:
		lg.ErrorC("Unsupported private key type", "type", fmt.Sprintf("%T", key))
	}

	keyFile := fmt.Sprintf("certs/%s_%s_key.pem", prefix, domain)
	if !isCertificateValid(keyFile, 1) {
		_ = os.Remove(keyFile)
	}
	err = os.WriteFile(keyFile, keyPEM, 0600)
	if lg.CheckError(err) {
		lg.Printfs("Failed to save private key: %v\n", err)
		return
	}
	lg.Printfs("Certificate %s and private key %s files for %s saved successfully.\n", certFile, keyFile, domain)
}

func isCertificateValid(certFile string, monthN int) bool {
	info, err := os.Stat(certFile)
	if err != nil {
		lg.Printfs("Failed to get certificate file info: %v\n", err)
		return false
	}
	// Check if the certificate file is older than 2 months
	twoMonthsAgo := time.Now().AddDate(0, -monthN, 0)
	return info.ModTime().After(twoMonthsAgo)
}

// Param is a single URL parameter, consisting of a key and a value.
type Param struct {
	Key   string
	Value string
}

// Params is a Param-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.
type Params []Param

// ByName returns the value of the first Param which key matches the given name.
// If no matching Param is found, an empty string is returned.
func (ps Params) ByName(name string) string {
	for _, p := range ps {
		if p.Key == name {
			return p.Value
		}
	}
	return ""
}

type paramsKey struct{}

// ctxKey is the request context key under which URL params are stored.
var ctxKey = paramsKey{}

// GetParamsFromCtx get params from ctx for http.Handler
func GetParamsFromCtx(ctx context.Context) Params {
	p, _ := ctx.Value(ctxKey).(Params)
	return p
}

// MatchedRoutePathParam is the Param name under which the path of the matched
// route is stored, if Router.SaveMatchedPath is set.
var MatchedRoutePathParam = "$ksmuxdone"

func (r *Router) getParams() *Params {
	ps, _ := r.paramsPool.Get().(*Params)
	*ps = (*ps)[:0] // reset slice
	return ps
}

func (r *Router) putParams(ps *Params) {
	if ps != nil {
		r.paramsPool.Put(ps)
	}
}

func (r *Router) saveMatchedRoutePath(path string, handler Handler) Handler {
	return func(c *Context) {
		ps := c.Params
		if ps == nil {
			psp := r.getParams()
			ps = (*psp)[0:1]
			ps[0] = Param{Key: MatchedRoutePathParam, Value: path}
			handler(c)
			r.putParams(psp)
		} else {
			c.Params = append(ps, Param{Key: MatchedRoutePathParam, Value: path})
			handler(c)
		}
	}
}

func (r *Router) recv(w http.ResponseWriter, req *http.Request) {
	if rcv := recover(); rcv != nil {
		r.PanicHandler(w, req, rcv)
	}
}

// Lookup allows the manual lookup of a method + path combo.
// This is e.g. useful to build a framework around this router.
// If the path was found, it returns the handler function and the path parameter
// values. Otherwise the third return value indicates whether a redirection to
// the same path with an extra / without the trailing slash should be performed.
func (r *Router) Lookup(method, path string) (Handler, Params, bool, string) {
	if root := r.trees[method]; root != nil {
		handler, ps, tsr, origines := root.getHandler(path, r.getParams)
		if handler == nil {
			r.putParams(ps)
			return nil, nil, tsr, origines
		}
		if ps == nil {
			return handler, nil, tsr, origines
		}
		return handler, *ps, tsr, origines
	}
	return nil, nil, false, ""
}

func (r *Router) allowed(path, reqMethod string) (allow string) {
	allowed := make([]string, 0, 9)

	if path == "*" { // server-wide
		// empty method is used for internal calls to refresh the cache
		if reqMethod == "" {
			for method := range r.trees {
				if method == http.MethodOptions {
					continue
				}
				// Add request method to list of allowed methods
				allowed = append(allowed, method)
			}
		} else {
			return r.globalAllowed
		}
	} else { // specific path
		for method := range r.trees {
			// Skip the requested method - we already tried this one
			if method == reqMethod || method == http.MethodOptions {
				continue
			}

			handler, _, _, _ := r.trees[method].getHandler(path, nil)
			if handler != nil {
				// Add request method to list of allowed methods
				allowed = append(allowed, method)
			}
		}
	}

	if len(allowed) > 0 {
		// Add request method to list of allowed methods
		if r.HandleOPTIONS {
			allowed = append(allowed, http.MethodOptions)
		}

		// Sort allowed methods.
		// sort.Strings(allowed) unfortunately causes unnecessary allocations
		// due to allowed being moved to the heap and interface conversion
		for i, l := 1, len(allowed); i < l; i++ {
			for j := i; j > 0 && allowed[j] < allowed[j-1]; j-- {
				allowed[j], allowed[j-1] = allowed[j-1], allowed[j]
			}
		}

		// return as comma separated list
		return strings.Join(allowed, ", ")
	}

	return allow
}

// Graceful Shutdown
func (router *Router) gracefulShutdown() {
	err := Graceful(func() error {
		// Shutdown server
		timeout, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := FuncBeforeServerShutdown(router.Server)
		if err != nil {
			return err
		}
		if router.Server != nil {
			if err := router.Server.Shutdown(timeout); err != nil {
				return err
			}
		}
		// else if certmagic.UsedHTTPServer != nil {
		// 	if err := certmagic.UsedHTTPServer.Shutdown(timeout); err != nil {
		// 		return err
		// 	}
		// }
		if limiterUsed {
			close(limiterQuit)
		}
		return nil
	})
	if err != nil {
		os.Exit(1)
	}
}

func Graceful(f func() error) error {
	s := make(chan os.Signal, 1)
	signal.Notify(s, os.Interrupt)
	<-s
	return f()
}

func checkDomain(name string) error {
	switch {
	case len(name) == 0:
		return nil
	case len(name) > 255:
		return fmt.Errorf("cookie domain: name length is %d, can't exceed 255", len(name))
	}
	var l int
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b == '.' {
			switch {
			case i == l:
				return fmt.Errorf("cookie domain: invalid character '%c' at offset %d: label can't begin with a period", b, i)
			case i-l > 63:
				return fmt.Errorf("cookie domain: byte length of label '%s' is %d, can't exceed 63", name[l:i], i-l)
			case name[l] == '-':
				return fmt.Errorf("cookie domain: label '%s' at offset %d begins with a hyphen", name[l:i], l)
			case name[i-1] == '-':
				return fmt.Errorf("cookie domain: label '%s' at offset %d ends with a hyphen", name[l:i], l)
			}
			l = i + 1
			continue
		}
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b >= 'A' && b <= 'Z') {
			// show the printable unicode character starting at byte offset i
			c, _ := utf8.DecodeRuneInString(name[i:])
			if c == utf8.RuneError {
				return fmt.Errorf("cookie domain: invalid rune at offset %d", i)
			}
			return fmt.Errorf("cookie domain: invalid character '%c' at offset %d", c, i)
		}
	}
	switch {
	case l == len(name):
		return fmt.Errorf("cookie domain: missing top level domain, domain can't end with a period")
	case len(name)-l > 63:
		return fmt.Errorf("cookie domain: byte length of top level domain '%s' is %d, can't exceed 63", name[l:], len(name)-l)
	case name[l] == '-':
		return fmt.Errorf("cookie domain: top level domain '%s' at offset %d begins with a hyphen", name[l:], l)
	case name[len(name)-1] == '-':
		return fmt.Errorf("cookie domain: top level domain '%s' at offset %d ends with a hyphen", name[l:], l)
	case name[l] >= '0' && name[l] <= '9':
		return fmt.Errorf("cookie domain: top level domain '%s' at offset %d begins with a digit", name[l:], l)
	}
	return nil
}

func resolveHostIp() string {
	netInterfaceAddresses, err := net.InterfaceAddrs()

	if err != nil {
		return ""
	}

	for _, netInterfaceAddress := range netInterfaceAddresses {
		networkIp, ok := netInterfaceAddress.(*net.IPNet)
		if ok && !networkIp.IP.IsLoopback() && networkIp.IP.To4() != nil {
			ip := networkIp.IP.String()
			return ip
		}
	}

	return ""
}

func getLocalPrivateIps() []string {
	ips := []string{}
	host, _ := os.Hostname()
	addrs, _ := net.LookupIP(host)
	for _, addr := range addrs {
		if ipv4 := addr.To4(); ipv4 != nil {
			ips = append(ips, ipv4.String())
		}
	}
	return ips
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	if localAddr.IP.To4().IsPrivate() {
		return localAddr.IP.String()
	}
	return ""
}

func GetPrivateIp() string {
	pIp := getOutboundIP()
	if pIp == "" {
		pIp = resolveHostIp()
		if pIp == "" {
			pIp = getLocalPrivateIps()[0]
		}
	}
	return pIp
}

var copyBufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 4096)
	},
}

func copyZeroAlloc(w io.Writer, r io.Reader) (int64, error) {
	vbuf := copyBufPool.Get()
	buf := vbuf.([]byte)
	n, err := io.CopyBuffer(w, r, buf)
	copyBufPool.Put(vbuf)
	return n, err
}

func StringContains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func SliceContains[T comparable](elems []T, vs ...T) bool {
	for _, s := range elems {
		for _, v := range vs {
			if v == s {
				return true
			}
		}
	}
	return false
}
