package connector

import (
	"net"
	"net/http"
	"os"
	"strconv"
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
// Proxy 字段留空，由调用方（如 resty 的 SetProxy）按渠道设置。
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
