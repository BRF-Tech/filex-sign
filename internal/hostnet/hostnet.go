// Package hostnet lets a library that insists on Go's own http.Client
// reach the outside through filex's host function instead.
//
// There is exactly one library in this plugin that does: digitorus/
// pdfsign calls an RFC 3161 time-stamping authority with its own
// `http.Client{}`, which a wasm guest has no sockets for. Rather than
// fork it, Install swaps http.DefaultTransport — the transport a zero
// http.Client uses — for one that hands the request to the host.
//
// ⚠ The host is the boundary, not this package: every call still needs
// an `http:<host>` grant in the manifest and the administrator's
// permission, private addresses are refused there, and the size and time
// limits are the host's. Installing this widens nothing.
package hostnet

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
)

// Install makes http.DefaultTransport go through the host. Call it from
// init(), before anything can hold a copy of the old transport.
func Install() { http.DefaultTransport = Transport{} }

// Transport is the http.RoundTripper over pluginkit.HTTPDo.
type Transport struct{}

// RoundTrip performs one request through the host.
func (Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("hostnet: no request")
	}
	var body []byte
	if req.Body != nil {
		defer req.Body.Close()
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		body = b
	}
	headers := map[string]string{}
	for k, v := range req.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	res, err := pluginkit.HTTPDo(pluginkit.HTTPRequest{
		Method: req.Method, URL: req.URL.String(), Headers: headers, Body: body,
	})
	if err != nil {
		return nil, err
	}
	out := &http.Response{
		StatusCode: res.Status,
		Status:     http.StatusText(res.Status),
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(res.Body)),
		Request:    req,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
	for k, v := range res.Headers {
		out.Header.Set(k, v)
	}
	if cl := out.Header.Get("Content-Length"); cl == "" {
		out.ContentLength = int64(len(res.Body))
	}
	return out, nil
}
