package capture

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// saveSubDir 是默认保存目录在「图片」文件夹下的子目录名。
const saveSubDir = "GoClock"

// DefaultSaveDir 返回默认的截图保存目录，即「图片\GoClock」。
func DefaultSaveDir() string {
	if dir, err := windows.KnownFolderPath(windows.FOLDERID_Pictures, 0); err == nil && dir != "" {
		return filepath.Join(dir, saveSubDir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Pictures", saveSubDir)
}

// FileName 按时间生成截图文件名，如 GoClock_20261104_153012.png。
func FileName(t time.Time) string {
	return FileNameExt(t, ".png")
}

// FileNameExt 按时间生成指定扩展名的文件名，如 GoClock_20261104_153012.mp4。
func FileNameExt(t time.Time, ext string) string {
	return "GoClock_" + t.Format("20060102_150405") + ext
}

// SavePNG 把图片写成 PNG 文件，目录不存在时自动创建。
func SavePNG(img image.Image, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败：%w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件失败：%w", err)
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("写入 PNG 失败：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("写入 PNG 失败：%w", err)
	}
	return nil
}

// SaveToDir 在目录 dir 下按时间命名保存图片，返回完整路径。
// 同一秒内连续保存时在文件名后追加序号，不会覆盖已有文件。
func SaveToDir(img image.Image, dir string, t time.Time) (string, error) {
	base := FileName(t)
	path := filepath.Join(dir, base)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, i, ext))
	}
	return path, SavePNG(img, path)
}
