package version

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// Proxy handles subdomain-based routing to version instances
type Proxy struct {
	manager    *Manager
	mainDomain string // e.g., "chatweb.ai"
	proxies    map[string]*httputil.ReverseProxy
	mu         sync.RWMutex
}

// NewProxy creates a new version proxy
func NewProxy(manager *Manager, mainDomain string) *Proxy {
	return &Proxy{
		manager:    manager,
		mainDomain: mainDomain,
		proxies:    make(map[string]*httputil.ReverseProxy),
	}
}

// GetVersionFromHost extracts version ID from subdomain
// e.g., "abc123.chatweb.ai" -> "abc123"
// e.g., "chatweb.ai" -> "" (main)
func (p *Proxy) GetVersionFromHost(host string) string {
	// Remove port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	// Check if it's a subdomain of our main domain
	if !strings.HasSuffix(host, "."+p.mainDomain) {
		return ""
	}

	// Extract subdomain
	subdomain := strings.TrimSuffix(host, "."+p.mainDomain)
	if subdomain == "" || subdomain == "www" {
		return ""
	}

	return subdomain
}

// GetProxyForVersion returns a reverse proxy for the given version
func (p *Proxy) GetProxyForVersion(versionID string) (*httputil.ReverseProxy, int, error) {
	v, ok := p.manager.GetVersion(versionID)
	if !ok {
		return nil, 0, nil
	}

	if v.Status != StatusRunning || v.Port == 0 {
		return nil, 0, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check cache
	key := versionID
	if proxy, exists := p.proxies[key]; exists {
		return proxy, v.Port, nil
	}

	// Create new proxy
	target, _ := url.Parse("http://localhost:" + fmt.Sprintf("%d", v.Port))
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom director to preserve original request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = req.URL.Host
	}

	p.proxies[key] = proxy
	return proxy, v.Port, nil
}

// ProxyRequest proxies an HTTP request to the appropriate version
func (p *Proxy) ProxyRequest(w http.ResponseWriter, r *http.Request) bool {
	versionID := p.GetVersionFromHost(r.Host)
	if versionID == "" {
		return false // Not a version subdomain
	}

	proxy, port, _ := p.GetProxyForVersion(versionID)
	if proxy == nil {
		http.Error(w, "Version not found or not running", http.StatusNotFound)
		return true
	}

	// Handle WebSocket upgrade
	if isWebSocketRequest(r) {
		p.proxyWebSocket(w, r, port)
		return true
	}

	proxy.ServeHTTP(w, r)
	return true
}

// proxyWebSocket handles WebSocket proxying
func (p *Proxy) proxyWebSocket(w http.ResponseWriter, r *http.Request, port int) {
	// Upgrade client connection
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	// Connect to backend
	backendURL := "ws://localhost:" + fmt.Sprintf("%d", port) + r.URL.Path
	backendConn, _, err := websocket.DefaultDialer.Dial(backendURL, nil)
	if err != nil {
		return
	}
	defer backendConn.Close()

	// Bidirectional copy
	done := make(chan struct{})

	go func() {
		defer close(done)
		copyWebSocket(clientConn, backendConn)
	}()

	copyWebSocket(backendConn, clientConn)
	<-done
}

func copyWebSocket(dst, src *websocket.Conn) {
	for {
		msgType, msg, err := src.ReadMessage()
		if err != nil {
			return
		}
		if err := dst.WriteMessage(msgType, msg); err != nil {
			return
		}
	}
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
}

// ClearProxyCache clears cached proxies (call when version stops)
func (p *Proxy) ClearProxyCache(versionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.proxies, versionID)
}

// ProxyHandler returns an http.Handler that proxies to versions
func (p *Proxy) ProxyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.ProxyRequest(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// copyIO is a helper for potential HTTP/2 proxying
func copyIO(dst io.Writer, src io.Reader) {
	io.Copy(dst, src)
}
