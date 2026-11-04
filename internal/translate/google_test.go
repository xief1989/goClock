package translate

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFreeResponse(t *testing.T) {
	// 取自接口的真实返回，第一段带换行。
	data := `[[["First line: Hello world.\n","第一行：你好世界。\n",null,null,3],["Second row.","第二行。",null,null,3]],null,"zh-CN",null,null,null,1,[],[["zh-CN"],null,[1],["zh-CN"]]]`
	text, src, err := parseFreeResponse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if text != "First line: Hello world.\nSecond row." || src != "zh-CN" {
		t.Errorf("解析结果不对：%q %q", text, src)
	}
	if _, _, err := parseFreeResponse([]byte("<html>blocked</html>")); err == nil {
		t.Error("不是 JSON 时应当报错")
	}
}

func TestParseOfficialResponse(t *testing.T) {
	data := `{"data":{"translations":[{"translatedText":"你好","detectedSourceLanguage":"en"}]}}`
	text, src, err := parseOfficialResponse([]byte(data))
	if err != nil || text != "你好" || src != "en" {
		t.Errorf("解析结果不对：%q %q %v", text, src, err)
	}
}

func TestSplitChunks(t *testing.T) {
	text := strings.Repeat("一二三四五\n", 10) // 每行 6 个字符（含换行）
	chunks := splitChunks(text, 20)
	if strings.Join(chunks, "") != text {
		t.Fatal("切分后拼回去应当与原文一致")
	}
	for _, c := range chunks {
		if n := len([]rune(c)); n > 20 {
			t.Errorf("每段不应超过 20 个字符：%d", n)
		}
	}
	long := strings.Repeat("长", 45)
	got := splitChunks(long, 20)
	if len(got) != 3 || strings.Join(got, "") != long {
		t.Errorf("超长单行应当硬切：%d 段", len(got))
	}
}

// roundTrip 是测试用的假网络，按请求的目标语言返回固定内容，并记录请求次数。
type roundTrip struct {
	calls   []string
	respond func(target string) string
}

func (r *roundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.URL.Query().Get("tl")
	r.calls = append(r.calls, target)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(r.respond(target))), Header: http.Header{}}, nil
}

func TestAutoTargetSwitchesToEnglishForChinese(t *testing.T) {
	rt := &roundTrip{respond: func(target string) string {
		if target == "en" {
			return `[[["Hello","你好",null,null,1]],null,"zh-CN"]`
		}
		return `[[["你好","你好",null,null,1]],null,"zh-CN"]`
	}}
	c := &Client{HTTP: &http.Client{Transport: rt}}
	res, err := c.Translate(context.Background(), "你好", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || res.Target != "en" {
		t.Errorf("中文原文在自动模式下应译成英文：%+v", res)
	}
	if strings.Join(rt.calls, ",") != "zh-CN,en" {
		t.Errorf("请求顺序不对：%v", rt.calls)
	}
}

func TestExplicitTargetIsKept(t *testing.T) {
	rt := &roundTrip{respond: func(target string) string {
		return `[[["こんにちは","你好",null,null,1]],null,"zh-CN"]`
	}}
	c := &Client{HTTP: &http.Client{Transport: rt}}
	res, err := c.Translate(context.Background(), "你好", "ja")
	if err != nil || res.Target != "ja" || len(rt.calls) != 1 {
		t.Errorf("指定目标语言时不应自动切换：%+v %v %v", res, rt.calls, err)
	}
}

// TestLiveFreeEndpoint 真实调用一次免 Key 接口，默认跳过：
//
//	$env:GOCLOCK_NET_TEST="1"; go test ./internal/translate -run TestLiveFreeEndpoint -v
func TestLiveFreeEndpoint(t *testing.T) {
	if os.Getenv("GOCLOCK_NET_TEST") == "" {
		t.Skip("未设置 GOCLOCK_NET_TEST，跳过联网测试")
	}
	c := &Client{HTTP: &http.Client{Timeout: 15 * time.Second}}
	res, err := c.Translate(context.Background(), "长截图拼接测试\n第二行文字", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s → %s：%q", res.Source, res.Target, res.Text)
	if res.Target != "en" || !strings.Contains(res.Text, "\n") {
		t.Errorf("中文应译成英文且保留换行：%+v", res)
	}
}
