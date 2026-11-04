package httpx

import (
	"net/http"
	"testing"
	"time"
)

func TestProxyForScheme(t *testing.T) {
	cases := []struct {
		server, scheme, want string
	}{
		{"10.0.0.1:8080", "https", "10.0.0.1:8080"},
		{"http=h1:1;https=h2:2", "https", "h2:2"},
		{"http=h1:1;https=h2:2", "http", "h1:1"},
		{"HTTP=h1:1", "http", "h1:1"},
		{"socks=s:1080", "https", "socks5://s:1080"},
		{"ftp=f:21", "https", ""},
	}
	for _, c := range cases {
		if got := proxyForScheme(c.server, c.scheme); got != c.want {
			t.Errorf("proxyForScheme(%q,%q)：got=%q want=%q", c.server, c.scheme, got, c.want)
		}
	}
}

func TestBypassed(t *testing.T) {
	list := "*.corp.com;10.*;<local>"
	cases := map[string]bool{
		"a.corp.com":          true,
		"10.9.3.30":           true,
		"intranet":            true,
		"translate.google.cn": false,
		"corp.com":            false,
	}
	for host, want := range cases {
		if got := bypassed(host, list); got != want {
			t.Errorf("bypassed(%q)：got=%v want=%v", host, got, want)
		}
	}
}

func TestSystemProxyFunc(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://translate.googleapis.com/x", nil)
	f := systemProxyFunc(systemProxy{enabled: true, server: "http=a:1;https=b:2", bypass: "<local>"})
	u, err := f(req)
	if err != nil || u == nil || u.Host != "b:2" {
		t.Fatalf("应走 https 代理 b:2：got=%v err=%v", u, err)
	}
	local, _ := http.NewRequest("GET", "http://intranet/x", nil)
	if u, _ := f(local); u != nil {
		t.Errorf("内网主机不应走代理：%v", u)
	}
}

func TestNewClientRejectsBadProxy(t *testing.T) {
	if _, err := NewClient("http://", time.Second); err == nil {
		t.Error("不完整的代理地址应当报错")
	}
	c, err := NewClient("127.0.0.1:7890", time.Second)
	if err != nil {
		t.Fatalf("省略协议的代理地址应当可用：%v", err)
	}
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	u, _ := c.Transport.(*http.Transport).Proxy(req)
	if u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Errorf("手填代理没有生效：%v", u)
	}
}
