// Package urls is the only place that builds absolute URLs (mail links, redirects). Every URL
// derives from PUBLIC_BASE_URL: the root host, "<slug>.<host>" for a tenant and "admin.<host>"
// for the platform console, keeping scheme and port so dev hosts like
// http://demo.localhost:5173 work unchanged.
package urls

import (
	"errors"
	"net/url"
	"strings"
)

// AdminLabel is the host label of the platform admin console.
const AdminLabel = "admin"

// Builder derives absolute URLs from the public base URL.
type Builder struct {
	scheme string
	host   string // hostname without port, lower case
	port   string
}

// New validates base (scheme http/https, a host, no path beyond "/", no query or user info).
func New(base *url.URL) (Builder, error) {
	if base == nil {
		return Builder{}, errors.New("public base URL is missing")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return Builder{}, errors.New("public base URL must use http or https")
	}
	if base.Hostname() == "" {
		return Builder{}, errors.New("public base URL has no host")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return Builder{}, errors.New("public base URL must be scheme://host[:port] only")
	}
	return Builder{
		scheme: base.Scheme,
		host:   strings.ToLower(base.Hostname()),
		port:   base.Port(),
	}, nil
}

// Parse is New for a raw string.
func Parse(raw string) (Builder, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Builder{}, errors.New("public base URL is not a valid URL")
	}
	return New(u)
}

// Host returns the root hostname (no port), e.g. "sidecup.app" or "localhost".
func (b Builder) Host() string { return b.host }

// Root returns an absolute URL on the root host.
func (b Builder) Root(path string) string { return b.build(b.host, path) }

// Tenant returns an absolute URL on the tenant host "<slug>.<host>".
func (b Builder) Tenant(slug, path string) string { return b.build(slug+"."+b.host, path) }

// Admin returns an absolute URL on the admin host "admin.<host>".
func (b Builder) Admin(path string) string { return b.build(AdminLabel+"."+b.host, path) }

func (b Builder) build(host, path string) string {
	if b.port != "" {
		host += ":" + b.port
	}
	u := url.URL{Scheme: b.scheme, Host: host}
	p, query, _ := strings.Cut(path, "?")
	if p == "" || p[0] != '/' {
		p = "/" + p
	}
	u.Path = p
	u.RawQuery = query
	return u.String()
}
