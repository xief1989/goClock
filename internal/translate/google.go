// Package translate 调用 Google 翻译把文字译成目标语言。
//
// 有两条路：不填 API Key 时走免 Key 的 translate.googleapis.com（非官方接口，可能被限流）；
// 填了 Key 走官方 Cloud Translation v2（收费，稳定）。两者都会把原文发给 Google。
package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	freeEndpoint     = "https://translate.googleapis.com/translate_a/single"
	officialEndpoint = "https://translation.googleapis.com/language/translate/v2"
	// maxChunkRunes 是每次请求最多携带的字符数，长文本按行切成多段依次翻译。
	maxChunkRunes = 4000
)

// Language 是界面上可选的一种目标语言。
type Language struct {
	// Code 是 Google 的语言代码，空字符串表示自动。
	Code string
	// Name 是界面上显示的名称。
	Name string
}

// Languages 是界面上可选的目标语言，第一项是「自动」。
var Languages = []Language{
	{"", "自动（中文译成英文，其余译成中文）"},
	{"zh-CN", "简体中文"},
	{"zh-TW", "繁体中文"},
	{"en", "英语"},
	{"ja", "日语"},
	{"ko", "韩语"},
	{"fr", "法语"},
	{"de", "德语"},
	{"ru", "俄语"},
	{"es", "西班牙语"},
}

// Result 是一次翻译的结果。
type Result struct {
	// Text 是译文。
	Text string
	// Source 是检测出的原文语言代码。
	Source string
	// Target 是实际使用的目标语言代码。
	Target string
}

// Client 是 Google 翻译客户端。
type Client struct {
	// HTTP 是联网用的客户端，代理由调用方配置。
	HTTP *http.Client
	// APIKey 为空时走免 Key 接口。
	APIKey string
}

// Translate 把 text 译成 target；target 为空时自动决定：原文是中文译成英文，否则译成中文。
func (c *Client) Translate(ctx context.Context, text, target string) (Result, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Result{}, errors.New("没有要翻译的文字")
	}
	chunks := splitChunks(text, maxChunkRunes)

	auto := target == ""
	if auto {
		target = "zh-CN"
	}
	first, source, err := c.translateChunk(ctx, chunks[0], target)
	if err != nil {
		return Result{}, err
	}
	if auto && isChinese(source) {
		// 原文本身就是中文，按约定改译成英文。
		target = "en"
		if first, source, err = c.translateChunk(ctx, chunks[0], target); err != nil {
			return Result{}, err
		}
	}

	var b strings.Builder
	b.WriteString(first)
	for _, chunk := range chunks[1:] {
		part, _, err := c.translateChunk(ctx, chunk, target)
		if err != nil {
			return Result{}, err
		}
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString(part)
	}
	return Result{Text: strings.TrimSpace(b.String()), Source: source, Target: target}, nil
}

// isChinese 判断语言代码是否属于中文（含简繁）。
func isChinese(code string) bool {
	code = strings.ToLower(code)
	return code == "zh" || strings.HasPrefix(code, "zh-") || strings.HasPrefix(code, "zh_")
}

// translateChunk 翻译一段文字，返回译文与检测出的原文语言。
func (c *Client) translateChunk(ctx context.Context, text, target string) (string, string, error) {
	if c.APIKey != "" {
		return c.official(ctx, text, target)
	}
	return c.free(ctx, text, target)
}

// free 调用免 Key 接口。原文放在表单里 POST，避免长文本撑爆 URL。
func (c *Client) free(ctx context.Context, text, target string) (string, string, error) {
	q := url.Values{"client": {"gtx"}, "sl": {"auto"}, "tl": {target}, "dt": {"t"}}
	body := url.Values{"q": {text}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, freeEndpoint+"?"+q.Encode(), strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	data, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	return parseFreeResponse(data)
}

// parseFreeResponse 解析免 Key 接口的返回：顶层是数组，[0] 是若干 [译文, 原文, ...] 片段，[2] 是检测出的语言。
func parseFreeResponse(data []byte) (string, string, error) {
	var top []json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || len(top) == 0 {
		return "", "", fmt.Errorf("翻译接口返回的内容看不懂：%s", abbreviate(data))
	}
	var segments []json.RawMessage
	if err := json.Unmarshal(top[0], &segments); err != nil {
		return "", "", fmt.Errorf("翻译接口返回的内容看不懂：%s", abbreviate(data))
	}
	var b strings.Builder
	for _, seg := range segments {
		var parts []json.RawMessage
		if json.Unmarshal(seg, &parts) != nil || len(parts) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(parts[0], &s) == nil {
			b.WriteString(s)
		}
	}
	var source string
	if len(top) > 2 {
		_ = json.Unmarshal(top[2], &source)
	}
	return b.String(), source, nil
}

// official 调用 Cloud Translation v2 接口。
func (c *Client) official(ctx context.Context, text, target string) (string, string, error) {
	payload, _ := json.Marshal(map[string]any{"q": []string{text}, "target": target, "format": "text"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		officialEndpoint+"?key="+url.QueryEscape(c.APIKey), bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	data, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	return parseOfficialResponse(data)
}

// parseOfficialResponse 解析 Cloud Translation v2 的返回。
func parseOfficialResponse(data []byte) (string, string, error) {
	var resp struct {
		Data struct {
			Translations []struct {
				TranslatedText         string `json:"translatedText"`
				DetectedSourceLanguage string `json:"detectedSourceLanguage"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Data.Translations) == 0 {
		return "", "", fmt.Errorf("翻译接口返回的内容看不懂：%s", abbreviate(data))
	}
	t := resp.Data.Translations[0]
	return t.TranslatedText, t.DetectedSourceLanguage, nil
}

// do 发送请求并读出返回内容，非 2xx 时把接口给的错误信息带出来。
func (c *Client) do(req *http.Request) ([]byte, error) {
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 Google 翻译：%w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("读取翻译结果失败：%w", err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return nil, fmt.Errorf("Google 翻译返回错误（%d）：%s", resp.StatusCode, e.Error.Message)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, errors.New("请求太频繁，被 Google 限流了，请稍后再试，或在设置里填 API Key")
		}
		return nil, fmt.Errorf("Google 翻译返回错误（%d）：%s", resp.StatusCode, abbreviate(data))
	}
	return data, nil
}

// splitChunks 按行把文本切成每段不超过 limit 个字符的若干段；单行超长时再按字符硬切。
func splitChunks(text string, limit int) []string {
	var chunks []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		n := utf8.RuneCountInString(line)
		if curLen+n > limit {
			flush()
		}
		for n > limit {
			r := []rune(line)
			chunks = append(chunks, string(r[:limit]))
			line = string(r[limit:])
			n -= limit
		}
		cur.WriteString(line)
		curLen += n
	}
	flush()
	if len(chunks) == 0 {
		chunks = []string{text}
	}
	return chunks
}

// abbreviate 截取一小段内容放进错误信息里。
func abbreviate(data []byte) string {
	s := strings.TrimSpace(string(data))
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}
