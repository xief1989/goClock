package storage

import (
	"os"
	"path/filepath"
	"testing"

	"goclock/internal/alarm"
)

// newTestStore 返回一个指向临时文件的 Store，不会碰到用户的真实配置。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{path: filepath.Join(t.TempDir(), "alarms.json")}
}

func TestLoadMissingFileReturnsSample(t *testing.T) {
	s := newTestStore(t)
	if !s.IsFirstRun() {
		t.Error("文件不存在时应判定为首次运行")
	}

	list, err := s.Load()
	if err != nil {
		t.Fatalf("首次加载不应报错：%v", err)
	}
	if len(list) == 0 {
		t.Fatal("首次运行应给出示例提醒")
	}
	if err := list[0].Validate(); err != nil {
		t.Errorf("示例提醒本身应当合法：%v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	want := &alarm.Alarm{
		ID: "a1", Title: "吃药", Message: "饭后一片",
		Enabled: true, RequireAck: true,
		Repeat: alarm.RepeatWeekly, Hour: 9, Minute: 30,
		Weekdays: []int{1, 3, 5},
	}
	if err := s.Save([]*alarm.Alarm{want}); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if s.IsFirstRun() {
		t.Error("保存之后不该再判定为首次运行")
	}

	list, err := s.Load()
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应读回 1 条：got=%d", len(list))
	}
	got := list[0]
	if got.Title != want.Title || got.Hour != want.Hour || got.Minute != want.Minute {
		t.Errorf("基本字段对不上：%+v", got)
	}
	if !got.RequireAck {
		t.Error("「必须手动关闭」属性没有保存下来")
	}
	if len(got.Weekdays) != 3 {
		t.Errorf("星期列表对不上：%v", got.Weekdays)
	}
}

func TestLoadLegacyConfigWithoutRequireAck(t *testing.T) {
	// 老版本写出的配置没有 requireAck 字段，读回来应当是 false，
	// 也就是保持「15 秒自动关闭就结束」的原有行为。
	s := newTestStore(t)
	legacy := `[{"id":"old","title":"旧提醒","enabled":true,"repeat":"daily","hour":8,"minute":0}]`
	if err := os.WriteFile(s.path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := s.Load()
	if err != nil {
		t.Fatalf("加载旧配置失败：%v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应读回 1 条：got=%d", len(list))
	}
	if list[0].RequireAck {
		t.Error("旧配置不该被解读成需要手动确认")
	}
	if list[0].Title != "旧提醒" {
		t.Errorf("标题对不上：%q", list[0].Title)
	}
}

func TestLoadToleratesBOM(t *testing.T) {
	// 用记事本另存为 UTF-8 会加上 BOM，不能因此解析失败。
	s := newTestStore(t)
	content := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`[{"id":"a","title":"带 BOM","enabled":true,"repeat":"daily","hour":7}]`)...)
	if err := os.WriteFile(s.path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := s.Load()
	if err != nil {
		t.Fatalf("带 BOM 的配置应当能读：%v", err)
	}
	if len(list) != 1 || list[0].Title != "带 BOM" {
		t.Errorf("读回内容不对：%+v", list)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(s.path, []byte("{ 这不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Error("损坏的配置应当报错，而不是当成空列表")
	}
}
