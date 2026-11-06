package connector

import "testing"

func TestValidateProxyURL(t *testing.T) {
	for _, tc := range []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"socks5://127.0.0.1:10808", false},
		{"socks5h://user:pass@proxy.example.com:1080", false},
		{"http://proxy.example.com:8080", false},
		{"https://proxy.example.com", false},
		{"socks5://", true},       // 缺 host
		{"ftp://proxy:21", true},  // 不支持的协议
		{"127.0.0.1:10808", true}, // 缺 scheme（Go 报 colon in first path segment）
		{"://broken", true},       // 解析失败
		{"socks5://host:port", true}, // 端口非数字，url.Parse 直接拒绝
	} {
		err := ValidateProxyURL(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateProxyURL(%q) = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
	}
}

func TestProxyTransportReusesCache(t *testing.T) {
	const proxy = "socks5://127.0.0.1:10808"
	a, err := ProxyTransport(proxy)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ProxyTransport(proxy)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("同一代理地址的 transport 应复用缓存，而不是每次新建")
	}
	if a.Proxy == nil {
		t.Fatal("代理 transport 未设置 Proxy 字段")
	}

	direct, err := ProxyTransport("")
	if err != nil {
		t.Fatal(err)
	}
	if direct.Proxy != nil {
		t.Fatal("直连 transport 不应设置 Proxy 字段")
	}

	for _, bad := range []string{"ftp://x:1", "socks5://", "not a valid :// url"} {
		if _, err := ProxyTransport(bad); err == nil {
			t.Errorf("ProxyTransport(%q) 应返回错误", bad)
		}
	}
}
