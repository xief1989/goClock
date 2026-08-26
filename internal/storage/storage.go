// Package storage 负责闹钟配置的本地持久化，数据以 JSON 形式保存在用户目录下。
package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"goclock/internal/alarm"
)

// appDirName 是配置目录在 %APPDATA% 下的名称。
const appDirName = "GoClock"

// configFileName 是闹钟配置文件名。
const configFileName = "alarms.json"

// Store 提供闹钟列表的读写，内部对文件操作加锁，可在多个协程中安全调用。
type Store struct {
	mu   sync.Mutex
	path string
}

// DataDir 返回并确保应用数据目录存在，通常为 %APPDATA%\GoClock。
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录：%w", err)
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("无法创建配置目录 %s：%w", dir, err)
	}
	return dir, nil
}

// New 创建一个指向默认配置文件的 Store。
func New() (*Store, error) {
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, configFileName)}, nil
}

// Path 返回配置文件的完整路径。
func (s *Store) Path() string {
	return s.path
}

// IsFirstRun 判断配置文件是否尚未创建，用于决定首次启动是否直接打开管理窗口。
func (s *Store) IsFirstRun() bool {
	_, err := os.Stat(s.path)
	return errors.Is(err, os.ErrNotExist)
}

// Load 读取闹钟列表。配置文件不存在时返回一组默认闹钟，方便首次使用。
func (s *Store) Load() ([]*alarm.Alarm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultAlarms(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败：%w", s.path, err)
	}

	// 用记事本另存为 UTF-8 时会带上 BOM，这里主动去掉以免解析失败。
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	var list []*alarm.Alarm
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("配置文件 %s 格式错误：%w", s.path, err)
	}

	// 过滤掉历史数据中可能出现的空项，并补齐缺失的标识。
	result := make([]*alarm.Alarm, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		if a.ID == "" {
			a.ID = alarm.New().ID
		}
		result = append(result, a)
	}
	return result, nil
}

// Save 以覆盖方式保存闹钟列表，先写临时文件再替换，避免写入中断损坏配置。
func (s *Store) Save(list []*alarm.Alarm) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if list == nil {
		list = []*alarm.Alarm{}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败：%w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败：%w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("保存配置失败：%w", err)
	}
	return nil
}

// defaultAlarms 返回首次运行时的示例闹钟，用户可以直接修改或删除。
func defaultAlarms() []*alarm.Alarm {
	a := alarm.New()
	a.Title = "起来活动一下"
	a.Message = "已经坐了很久了，站起来走两步、远眺一会儿。"
	a.Repeat = alarm.RepeatWorkday
	a.Hour = 10
	a.Minute = 30
	return []*alarm.Alarm{a}
}
