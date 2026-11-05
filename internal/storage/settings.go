package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// settingsFileName 是程序设置文件名，与闹钟数据分开存放，互不影响。
const settingsFileName = "settings.json"

// DefaultShotHotkey 是截图热键的默认值。
//
// 没有沿用微信的 Alt+A：微信运行时会先占住它，这边永远注册不上。
const DefaultShotHotkey = "Ctrl+Alt+A"

// Settings 是程序级设置。
// 新增字段时零值必须是合理的默认行为，这样旧版本写出的文件读回来不会出问题。
type Settings struct {
	// ShotHotkey 是截图热键，形如 "Ctrl+Alt+A"；为空表示不注册热键。
	ShotHotkey string `json:"shotHotkey"`
	// SaveDir 是截图保存目录，为空时使用默认的「图片\GoClock」。
	SaveDir string `json:"saveDir"`
	// AutoSave 表示完成截图（复制到剪贴板）时，同时保存一份 PNG 到保存目录。
	AutoSave bool `json:"autoSave"`

	// RecordFPS 是录屏帧率，0 表示默认的 30。
	RecordFPS int `json:"recordFps"`
	// RecordHideCursor 表示录屏时不画鼠标指针；默认画。
	RecordHideCursor bool `json:"recordHideCursor"`

	// TranslateTarget 是翻译的目标语言代码，如 "zh-CN"、"en"；为空表示自动（中文译成英文，其余译成中文）。
	TranslateTarget string `json:"translateTarget"`
	// GoogleAPIKey 是 Google Cloud Translation 的 API Key，经 EncryptSecret 加密后保存；为空表示用免 Key 接口。
	GoogleAPIKey string `json:"googleApiKey"`
	// Proxy 是联网用的代理地址，如 http://127.0.0.1:7890；为空表示跟随系统代理设置。
	Proxy string `json:"proxy"`

	// OCREngine 是识别文字用的引擎：为空表示高精度的 PaddleOCR（首次使用自动下载，不可用时改用系统 OCR），
	// OCREngineWindows 表示直接用系统自带的 OCR。
	OCREngine string `json:"ocrEngine"`
}

// OCREngineWindows 是 Settings.OCREngine 的取值：直接用系统自带的 OCR。
const OCREngineWindows = "windows"

// DefaultRecordFPS 是录屏的默认帧率。
const DefaultRecordFPS = 30

// EffectiveRecordFPS 返回实际使用的录屏帧率，未设置或不合理时用默认值。
func (s Settings) EffectiveRecordFPS() int {
	if s.RecordFPS < 5 || s.RecordFPS > 60 {
		return DefaultRecordFPS
	}
	return s.RecordFPS
}

// DefaultSettings 返回首次运行时使用的设置。
func DefaultSettings() Settings {
	return Settings{ShotHotkey: DefaultShotHotkey}
}

// SettingsStore 负责 settings.json 的读写，内部加锁，可在多个协程中调用。
type SettingsStore struct {
	mu   sync.Mutex
	path string
}

// NewSettingsStore 创建一个指向默认设置文件的 SettingsStore。
func NewSettingsStore() (*SettingsStore, error) {
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	return &SettingsStore{path: filepath.Join(dir, settingsFileName)}, nil
}

// Load 读取设置。文件不存在时返回默认设置。
func (s *SettingsStore) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return DefaultSettings(), fmt.Errorf("读取设置文件 %s 失败：%w", s.path, err)
	}

	// 和闹钟配置一样，兼容记事本另存时带上的 BOM。
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	v := DefaultSettings()
	if err := json.Unmarshal(data, &v); err != nil {
		return DefaultSettings(), fmt.Errorf("设置文件 %s 格式错误：%w", s.path, err)
	}
	return v, nil
}

// Save 以覆盖方式保存设置，先写临时文件再替换，避免写入中断损坏文件。
func (s *SettingsStore) Save(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化设置失败：%w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败：%w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("保存设置失败：%w", err)
	}
	return nil
}
