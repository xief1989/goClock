// 命令 genicon 生成 assets/icon.ico，供 rsrc 打包成可执行文件的图标资源。
// 图标内容与运行时托盘图标完全一致，都来自 internal/icon 的绘制代码。
//
// 用法：go run ./tools/genicon
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"goclock/internal/icon"
)

func main() {
	dir := "assets"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建目录失败：", err)
		os.Exit(1)
	}

	path := filepath.Join(dir, "icon.ico")
	data := icon.EncodeICO()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写入图标失败：", err)
		os.Exit(1)
	}

	fmt.Printf("已生成 %s（%d 字节，含 %d 种尺寸）\n", path, len(data), len(icon.Sizes))
}
