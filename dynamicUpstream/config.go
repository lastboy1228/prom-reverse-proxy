package dynamicUpstream

import (
	"net"
	"net/http"
	"time"
)

func ConfigHttpClientConnection() {
	// Customize the Transport to have larger connection pool
	defaultTransport := http.DefaultTransport.(*http.Transport)
	defaultTransport.MaxIdleConns = 200
	defaultTransport.MaxIdleConnsPerHost = 100
	defaultTransport.ResponseHeaderTimeout = time.Minute
	defaultTransport.DialContext = (&net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}).DialContext
}
