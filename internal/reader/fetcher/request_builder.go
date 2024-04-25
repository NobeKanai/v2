// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fetcher // import "miniflux.app/v2/internal/reader/fetcher"

import (
	"crypto/tls"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	defaultHTTPClientTimeout     = 20
	defaultHTTPClientMaxBodySize = 15 * 1024 * 1024
	defaultAcceptHeader          = "application/xml, application/atom+xml, application/rss+xml, application/rdf+xml, application/feed+json, text/html, */*;q=0.9"
)

type RequestBuilder struct {
	headers      http.Header
	clientConfig clientConfig
}

func NewRequestBuilder() *RequestBuilder {
	return &RequestBuilder{
		headers: make(http.Header),
		clientConfig: clientConfig{
			clientTimeout: defaultHTTPClientTimeout,
		},
	}
}

func (r *RequestBuilder) Copy() *RequestBuilder {
	rb := *r
	// make sure every fields are deep copied
	rb.headers = r.headers.Clone()
	return &rb
}

func (r *RequestBuilder) WithHeader(key, value string) *RequestBuilder {
	r.headers.Set(key, value)
	return r
}

func (r *RequestBuilder) WithETag(etag string) *RequestBuilder {
	if etag != "" {
		r.headers.Set("If-None-Match", etag)
	}
	return r
}

func (r *RequestBuilder) WithLastModified(lastModified string) *RequestBuilder {
	if lastModified != "" {
		r.headers.Set("If-Modified-Since", lastModified)
	}
	return r
}

func (r *RequestBuilder) WithUserAgent(userAgent string, defaultUserAgent string) *RequestBuilder {
	if userAgent != "" {
		r.headers.Set("User-Agent", userAgent)
	} else {
		r.headers.Set("User-Agent", defaultUserAgent)
	}
	return r
}

func (r *RequestBuilder) WithCookie(cookie string) *RequestBuilder {
	if cookie != "" {
		r.headers.Set("Cookie", cookie)
	}
	return r
}

func (r *RequestBuilder) WithUsernameAndPassword(username, password string) *RequestBuilder {
	if username != "" && password != "" {
		r.headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	}
	return r
}

func (r *RequestBuilder) WithProxy(proxyURL string) *RequestBuilder {
	r.clientConfig.clientProxyURL = proxyURL
	return r
}

func (r *RequestBuilder) UseProxy(value bool) *RequestBuilder {
	r.clientConfig.useClientProxy = value
	return r
}

func (r *RequestBuilder) WithTimeout(timeout int) *RequestBuilder {
	r.clientConfig.clientTimeout = timeout
	return r
}

func (r *RequestBuilder) WithoutRedirects() *RequestBuilder {
	r.clientConfig.withoutRedirects = true
	return r
}

func (r *RequestBuilder) DisableHTTP2(value bool) *RequestBuilder {
	r.clientConfig.disableHTTP2 = value
	return r
}

func (r *RequestBuilder) IgnoreTLSErrors(value bool) *RequestBuilder {
	r.clientConfig.ignoreTLSErrors = value
	return r
}

func (r *RequestBuilder) ExecuteRequest(requestURL string) (*http.Response, error) {
	client := makeClient(r.clientConfig)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header = r.headers
	req.Header.Set("Accept", defaultAcceptHeader)

	slog.Debug("Making outgoing request", slog.Group("request",
		slog.String("method", req.Method),
		slog.String("url", req.URL.String()),
		slog.Any("headers", req.Header),
		slog.Bool("without_redirects", r.clientConfig.withoutRedirects),
		slog.Bool("with_proxy", r.clientConfig.useClientProxy),
		slog.String("proxy_url", r.clientConfig.clientProxyURL),
		slog.Bool("ignore_tls_errors", r.clientConfig.ignoreTLSErrors),
		slog.Bool("disable_http2", r.clientConfig.disableHTTP2),
	))

	return client.Do(req)
}

type clientConfig struct {
	ignoreTLSErrors  bool
	disableHTTP2     bool
	useClientProxy   bool
	withoutRedirects bool
	clientProxyURL   string
	clientTimeout    int
}

var (
	clientCache = map[clientConfig]*http.Client{}
	cacheMutex  = &sync.Mutex{}
)

func makeClient(cfg clientConfig) *http.Client {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()

	if client, ok := clientCache[cfg]; ok {
		return client
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Setting `DialContext` disables HTTP/2, this option forces the transport to try HTTP/2 regardless.
		ForceAttemptHTTP2: true,
		DialContext: (&net.Dialer{
			// Default is 30s.
			Timeout: 10 * time.Second,

			// Default is 30s.
			KeepAlive: 15 * time.Second,
		}).DialContext,

		// Default is 100.
		MaxIdleConns: 50,

		// Default is 90s.
		IdleConnTimeout: 10 * time.Second,

		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.ignoreTLSErrors,
		},
	}

	if cfg.disableHTTP2 {
		transport.ForceAttemptHTTP2 = false

		// https://pkg.go.dev/net/http#hdr-HTTP_2
		// Programs that must disable HTTP/2 can do so by setting [Transport.TLSNextProto] (for clients) or [Server.TLSNextProto] (for servers) to a non-nil, empty map.
		transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}

	if cfg.useClientProxy && cfg.clientProxyURL != "" {
		if proxyURL, err := url.Parse(cfg.clientProxyURL); err != nil {
			slog.Warn("Unable to parse proxy URL",
				slog.String("proxy_url", cfg.clientProxyURL),
				slog.Any("error", err),
			)
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	client := &http.Client{
		Timeout: time.Duration(cfg.clientTimeout) * time.Second,
	}

	if cfg.withoutRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	client.Transport = transport

	clientCache[cfg] = client

	return client
}
