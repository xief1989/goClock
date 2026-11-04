// Package httpx 按程序设置生成联网用的 HTTP 客户端：手填了代理就用手填的，否则跟随系统代理。
package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

// internetSettingsKey 是「Internet 选项」里代理设置所在的注册表位置。
const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// NewClient 生成 HTTP 客户端。proxy 为空时跟随系统代理（Internet 选项里的手动代理，
// 没开就看 HTTPS_PROXY 等环境变量）；不支持自动配置脚本（PAC）。
func NewClient(proxy string, timeout time.Duration) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	proxy = strings.TrimSpace(proxy)
	if proxy != "" {
		u, err := parseProxyURL(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	} else {
		tr.Proxy = systemProxyFunc(readSystemProxy())
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

// parseProxyURL 解析手填的代理地址，省略协议时按 http 处理。
func parseProxyURL(s string) (*url.URL, error) {
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址「%s」格式不对，应形如 http://127.0.0.1:7890", s)
	}
	return u, nil
}

// systemProxy 是从注册表读出的系统代理设置。
type systemProxy struct {
	enabled bool
	server  string
	bypass  string
}

// readSystemProxy 读取当前用户「Internet 选项」里的手动代理设置，读不到时视为未开启。
func readSystemProxy() systemProxy {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return systemProxy{}
	}
	defer k.Close()
	var sp systemProxy
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		sp.enabled = v != 0
	}
	sp.server, _, _ = k.GetStringValue("ProxyServer")
	sp.bypass, _, _ = k.GetStringValue("ProxyOverride")
	return sp
}

// systemProxyFunc 把系统代理设置转成 http.Transport 用的代理函数。
func systemProxyFunc(sp systemProxy) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if !sp.enabled || sp.server == "" {
			return http.ProxyFromEnvironment(req)
		}
		if bypassed(req.URL.Hostname(), sp.bypass) {
			return nil, nil
		}
		server := proxyForScheme(sp.server, req.URL.Scheme)
		if server == "" {
			return nil, nil
		}
		return parseProxyURL(server)
	}
}

// proxyForScheme 从 ProxyServer 的值里挑出某个协议对应的代理。
// 值有两种写法：「host:port」对所有协议生效；「http=h1:p1;https=h2:p2;socks=h3:p3」按协议分开。
func proxyForScheme(server, scheme string) string {
	server = strings.TrimSpace(server)
	if !strings.Contains(server, "=") {
		return server
	}
	var socks string
	for _, part := range strings.Split(server, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case strings.ToLower(scheme):
			return v
		case "socks":
			socks = "socks5://" + v
		}
	}
	return socks
}

// bypassed 判断主机是否在「不使用代理」的例外列表里。
// 列表用分号分隔，支持 * 通配；「<local>」表示不含点号的内网主机名。
func bypassed(host, list string) bool {
	host = strings.ToLower(host)
	for _, p := range strings.Split(list, ";") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
		case p == "<local>":
			if !strings.Contains(host, ".") {
				return true
			}
		default:
			if ok, _ := path.Match(p, host); ok {
				return true
			}
		}
	}
	return false
}
