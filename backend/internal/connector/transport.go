package connector

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// 上游访问的 HTTP 超时配置。
//
// 默认值针对"上游站点在境外、且部分渠道走 socks5 代理"的场景留足余量：
// Go 的 http.DefaultTransport 里 TLSHandshakeTimeout 只有 10s，链路一抖动就会直接
// 报 "net/http: TLS handshake timeout"，请求根本没发出去。这里统一放宽到 30s。
//
// 可用环境变量覆盖（单位：秒）：
//
//	UPSTREAMHUB_HTTP_DIAL_TIMEOUT            拨号超时，默认 20
//	UPSTREAMHUB_HTTP_TLS_HANDSHAKE_TIMEOUT  TLS 握手超时，默认 30
//	UPSTREAMHUB_HTTP_RESPONSE_HEADER_TIMEOUT 响应首字节超时，默认 30
//	UPSTREAMHUB_HTTP_REQUEST_TIMEOUT         单次请求总超时，默认 120
const (
	defaultDialTimeout           = 20 * time.Second
	defaultTLSHandshakeTimeout   = 30 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultRequestTimeout        = 120 * time.Second
)

// envSeconds 读取秒数型环境变量，非法或缺省时返回 def。
func envSeconds(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return time.Duration(n) * time.Second
}

// DialTimeout 拨号超时。
func DialTimeout() time.Duration {
	return envSeconds("UPSTREAMHUB_HTTP_DIAL_TIMEOUT", defaultDialTimeout)
}

// TLSHandshakeTimeout TLS 握手超时。
func TLSHandshakeTimeout() time.Duration {
	return envSeconds("UPSTREAMHUB_HTTP_TLS_HANDSHAKE_TIMEOUT", defaultTLSHandshakeTimeout)
}

// ResponseHeaderTimeout 等待响应头的超时。
func ResponseHeaderTimeout() time.Duration {
	return envSeconds("UPSTREAMHUB_HTTP_RESPONSE_HEADER_TIMEOUT", defaultResponseHeaderTimeout)
}

// RequestTimeout 单次请求总超时。
func RequestTimeout() time.Duration {
	return envSeconds("UPSTREAMHUB_HTTP_REQUEST_TIMEOUT", defaultRequestTimeout)
}

// NewHTTPTransport 构造带宽松超时的 http.Transport。
//
// Proxy 字段留空，由调用方（如 ProxyTransport）按渠道设置。
func NewHTTPTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   DialTimeout(),
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   TLSHandshakeTimeout(),
		ResponseHeaderTimeout: ResponseHeaderTimeout(),
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// allowedProxySchemes 渠道代理允许的 URL scheme（net/http Transport 原生支持的集合）。
var allowedProxySchemes = map[string]struct{}{
	"http": {}, "https": {}, "socks5": {}, "socks5h": {},
}

// ValidateProxyURL 校验渠道代理地址格式，避免非法值等到扫描时才报出
// 难懂的 proxyconnect 错误。空串合法（等价直连）。
func ValidateProxyURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("代理地址无法解析：%v", err)
	}
	if _, ok := allowedProxySchemes[u.Scheme]; !ok {
		return fmt.Errorf("代理协议不支持：%q（支持 http / https / socks5 / socks5h）", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("代理地址缺少主机和端口")
	}
	return nil
}

// proxyTransportCache 按代理 URL 缓存共享 transport。
//
// 配置了代理的渠道历史上每个请求都新建 transport + resty 客户端，
// 意味着每次请求都要重做一遍代理握手 + TLS 握手，连接完全无法复用。
// 这里按代理地址缓存 *http.Transport，让同一代理下的请求共享连接池。
var proxyTransportCache sync.Map // string -> *http.Transport

// ProxyTransport 返回走指定代理的共享 *http.Transport（按代理地址缓存复用）。
// transport 内部已设置 Proxy 字段，调用方无需再走 resty 的 SetProxy。
func ProxyTransport(raw string) (*http.Transport, error) {
	if raw == "" {
		// 直连场景由调用方走共享直连客户端，这里只做兜底，不缓存。
		return NewHTTPTransport(), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy url: %w", err)
	}
	if _, ok := allowedProxySchemes[u.Scheme]; !ok {
		return nil, fmt.Errorf("proxy scheme %q not supported", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy url %q missing host", raw)
	}
	if v, ok := proxyTransportCache.Load(raw); ok {
		return v.(*http.Transport), nil
	}
	tr := NewHTTPTransport()
	tr.Proxy = http.ProxyURL(u)
	// LoadOrStore 防并发重复建；落败的那份直接丢弃，未建过连接无泄漏。
	actual, _ := proxyTransportCache.LoadOrStore(raw, tr)
	return actual.(*http.Transport), nil
}
