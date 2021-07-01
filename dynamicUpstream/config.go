package dynamicUpstream

import (
	"net"
	"net/http"
	"net/url"
	"time"
)

func ConfigHttpClientConnection() {
	// Customize the Transport to have larger connection pool
	defaultTransport := http.DefaultTransport.(*http.Transport)
	defaultTransport.MaxIdleConns = 200
	defaultTransport.MaxIdleConnsPerHost = 100
	defaultTransport.ResponseHeaderTimeout = time.Minute
	defaultTransport.Proxy = noProxy
	defaultTransport.DialContext = (&net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}).DialContext
}

func noProxy(req *http.Request) (*url.URL, error) {
	return nil, nil
}
