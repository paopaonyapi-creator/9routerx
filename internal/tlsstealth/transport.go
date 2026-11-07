// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package tlsstealth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

// clientCache holds one *http.Client per profile. The handshake state and the
// connection pool live in the client, so building one per call would throw
// both away — the same reason the executor packages key their stealth clients
// by proxy URL. Guarded rather than sync.Map: the set of keys is tiny and
// read-mostly, and a plain map keeps the lookup honest.
var (
	clientCacheMu sync.Mutex
	clientCache   = map[string]*http.Client{}
)

// Transport builds an *http.Client whose TLS ClientHello and HTTP/2 settings
// are the named profile's, not Go's net/http's.
//
// It works whether or not the flag is on: impersonation here is opt-in per
// call, and the flag gates only the web-executor wiring and the alert loop
// (see WebExecutorClient). An empty or unknown name is an error — a nil
// profile falling through to Go's own handshake would silently un-stealth
// the caller, which is exactly the failure the profile exists to prevent.
func Transport(profile string) (*http.Client, error) {
	p, ok := profilesByName[profile]
	if !ok {
		return nil, fmt.Errorf("tlsstealth: unknown profile %q (want one of %s)",
			profile, strings.Join(profileNames(), ", "))
	}
	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()
	if cached, ok := clientCache[p.Name]; ok {
		return cached, nil
	}
	inner, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), clientOptions(p)...)
	if err != nil {
		return nil, fmt.Errorf("tlsstealth: build %s client: %w", p.Name, err)
	}
	client := &http.Client{Transport: &stealthRoundTripper{inner: inner}}
	clientCache[p.Name] = client
	return client, nil
}

// clientOptions is the dial configuration every profile shares, mirroring the
// executor packages' stealth clients (deepseek_web_transport.go,
// gemini_web_stealth.go) so the whole lane presents the same shape.
func clientOptions(p Profile) []tls_client.HttpClientOption {
	options := []tls_client.HttpClientOption{
		// No overall timeout: the web lane streams, and the two clocks that
		// matter — first byte and idle — are the caller's to enforce. A
		// deadline here would cut a legitimately long answer short.
		tls_client.WithTimeoutSeconds(0),
		tls_client.WithClientProfile(p.clientProfile),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
		tls_client.WithDialer(net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}),
		// The web lane answers JSON and magic-prefixed line streams, not
		// redirect chains; following one would hide the real response behind
		// a login page.
		tls_client.WithNotFollowRedirects(),
	}
	if p.shuffleExtensions {
		options = append(options, tls_client.WithRandomTLSExtensionOrder())
	}
	return options
}

// stealthRoundTripper adapts net/http requests onto the tls-client stack, so
// callers keep working with the plain *http.Client the rest of the codebase
// passes around.
//
// Why an adapter at all: tls-client speaks its own fhttp fork of net/http,
// and its HttpClient is not an http.RoundTripper. The conversion is shallow
// and lossless for everything the web lane sends — method, URL, headers,
// body, context — except header order, which net/http does not carry and
// fhttp only gets from the HeaderOrderKey an ordered caller sets itself.
// The executors that need ordering build their own fhttp requests; this
// transport exists for the ClientHello, which is per-connection, not
// per-header.
type stealthRoundTripper struct {
	inner tls_client.HttpClient
}

// RoundTrip converts one request, sends it, and converts the response back.
func (t *stealthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	inner, err := toFhttpRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := t.inner.Do(inner)
	if err != nil {
		return nil, err
	}
	return toNetResponse(resp), nil
}

// toFhttpRequest reshapes a net/http request into the fhttp fork. Header and
// trailer maps are cloned: the tls-client's cookie jar and hooks write into
// the header map it is handed, and the caller's request must not gain a
// cookie it never sent.
func toFhttpRequest(req *http.Request) (*fhttp.Request, error) {
	out := &fhttp.Request{
		Method:        req.Method,
		URL:           req.URL,
		Proto:         req.Proto,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		Header:        fhttp.Header(req.Header).Clone(),
		Body:          req.Body,
		GetBody:       req.GetBody,
		ContentLength: req.ContentLength,
		Host:          req.Host,
	}
	if req.Trailer != nil {
		out.Trailer = fhttp.Header(req.Trailer).Clone()
	}
	return out.WithContext(req.Context()), nil
}

// toNetResponse reshapes an fhttp response into the net/http shape every
// caller in the codebase reads, the same conversion the executor packages
// apply at their send boundary.
func toNetResponse(resp *fhttp.Response) *http.Response {
	header := make(http.Header, len(resp.Header))
	for name, values := range resp.Header {
		header[name] = values
	}
	out := &http.Response{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		Header:        header,
		Body:          resp.Body,
		ContentLength: resp.ContentLength,
	}
	if len(resp.Trailer) > 0 {
		out.Trailer = http.Header(resp.Trailer)
	}
	return out
}
