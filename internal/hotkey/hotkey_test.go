package hotkey

import "testing"

func TestParseAndFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Ctrl+Alt+A", "Ctrl+Alt+A"},
		{" ctrl + shift + x ", "Ctrl+Shift+X"},
		{"Alt+Ctrl+1", "Ctrl+Alt+1"},
		{"F1", "F1"},
		{"Shift+F12", "Shift+F12"},
		{"PrtSc", "PrintScreen"},
		{"Win+Shift+S", "Shift+Win+S"},
		{"Ctrl+Space", "Ctrl+Space"},
	}
	for _, c := range cases {
		combo, err := Parse(c.in)
		if err != nil {
			t.Errorf("%q 解析失败：%v", c.in, err)
			continue
		}
		if got := combo.String(); got != c.want {
			t.Errorf("%q 格式化结果：got=%q want=%q", c.in, got, c.want)
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	bad := []string{
		"",         // 空
		"A",        // 普通字母必须带修饰键
		"Ctrl+Alt", // 缺主键
		"Ctrl++A",  // 空段
		"A+Ctrl",   // 主键不在最后
		"Ctrl+Foo", // 不认识的键
		"Ctrl+F25", // 超出范围
		"Ctrl+F01", // 写法不规范
		"Ctrl+AB",  // 多字母
		"Space",    // 普通按键单独使用
	}
	for _, in := range bad {
		if _, err := Parse(in); err == nil {
			t.Errorf("%q 应当被拒绝", in)
		}
	}
}
