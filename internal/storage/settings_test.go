package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestSettingsStore 返回一个指向临时文件的 SettingsStore，不会碰到用户的真实设置。
func newTestSettingsStore(t *testing.T) *SettingsStore {
	t.Helper()
	return &SettingsStore{path: filepath.Join(t.TempDir(), "settings.json")}
}

func TestSettingsMissingFileReturnsDefaults(t *testing.T) {
	s := newTestSettingsStore(t)
	v, err := s.Load()
	if err != nil {
		t.Fatalf("文件不存在时不应报错：%v", err)
	}
	if v.ShotHotkey != DefaultShotHotkey {
		t.Errorf("默认热键应为 %s：got=%q", DefaultShotHotkey, v.ShotHotkey)
	}
	if v.AutoSave {
		t.Error("默认不应自动保存")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestSettingsStore(t)
	want := Settings{ShotHotkey: "Ctrl+Shift+X", SaveDir: `D:\shots`, AutoSave: true, OCREngine: OCREngineWindows}
	if err := s.Save(want); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if got != want {
		t.Errorf("读回内容不一致：got=%+v want=%+v", got, want)
	}
}

func TestSettingsEmptyHotkeyIsKept(t *testing.T) {
	// 用户主动清空热键表示「不用热键」，不能在读回时又被默认值顶掉。
	s := newTestSettingsStore(t)
	if err := s.Save(Settings{ShotHotkey: ""}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShotHotkey != "" {
		t.Errorf("清空的热键应保持为空：got=%q", got.ShotHotkey)
	}
}

func TestSettingsMissingFieldUsesDefault(t *testing.T) {
	// 文件里没有某个字段时按默认值处理，便于以后加字段。
	s := newTestSettingsStore(t)
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"autoSave":true}`)...)
	if err := os.WriteFile(s.path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("带 BOM 的设置应当能读：%v", err)
	}
	// 旧版本写出的文件没有 ocrEngine，读回来应是默认的高精度识别。
	if got.ShotHotkey != DefaultShotHotkey || !got.AutoSave || got.OCREngine != "" {
		t.Errorf("读回内容不对：%+v", got)
	}
}

func TestEffectiveRecordFPS(t *testing.T) {
	cases := map[int]int{0: 30, 15: 15, 24: 24, 60: 60, 61: 30, -1: 30, 3: 30}
	for in, want := range cases {
		if got := (Settings{RecordFPS: in}).EffectiveRecordFPS(); got != want {
			t.Errorf("RecordFPS=%d：got=%d want=%d", in, got, want)
		}
	}
}

func TestSecretRoundTrip(t *testing.T) {
	enc, err := EncryptSecret("AIza-测试-key")
	if err != nil {
		t.Fatalf("加密失败：%v", err)
	}
	if enc == "AIza-测试-key" || enc == "" {
		t.Fatalf("加密结果不应是明文：%q", enc)
	}
	got, err := DecryptSecret(enc)
	if err != nil || got != "AIza-测试-key" {
		t.Fatalf("解密结果不对：got=%q err=%v", got, err)
	}
	if v, _ := DecryptSecret("plain-key"); v != "plain-key" {
		t.Errorf("没有前缀的值应按明文返回：%q", v)
	}
	if _, err := DecryptSecret("dpapi:@@@"); err == nil {
		t.Error("坏的密文应当报错")
	}
	if v, err := EncryptSecret(""); v != "" || err != nil {
		t.Errorf("空值应原样返回：%q %v", v, err)
	}
}

func TestSettingsBrokenJSONFallsBack(t *testing.T) {
	s := newTestSettingsStore(t)
	if err := os.WriteFile(s.path, []byte("{ 坏掉了"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err == nil {
		t.Error("损坏的设置文件应当报错")
	}
	if got.ShotHotkey != DefaultShotHotkey {
		t.Errorf("出错时应退回默认设置：%+v", got)
	}
}
