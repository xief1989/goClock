package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

// TestORTLoadsModels 加载 ONNX Runtime 与 PP-OCRv5 两个模型，检查输入输出名与字典元数据。
// 需要事先准备好运行库与模型，默认跳过：
//
//	$env:GOCLOCK_PADDLE_DIR="D:\paddle"; go test ./internal/ocr -run TestORTLoadsModels -v
func TestORTLoadsModels(t *testing.T) {
	dir := os.Getenv("GOCLOCK_PADDLE_DIR")
	if dir == "" {
		t.Skip("未设置 GOCLOCK_PADDLE_DIR，跳过")
	}
	rt, err := loadORT(filepath.Join(dir, "onnxruntime.dll"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.close()
	t.Logf("ONNX Runtime %s", rt.version)
	for _, name := range []string{detModelFile, recModelFile} {
		if name == "" {
			continue
		}
		s, err := rt.openSession(filepath.Join(dir, name), 0)
		if err != nil {
			t.Fatal(err)
		}
		chars, err := s.metadata("character")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s 输入 %v 输出 %v 字典 %d 字节", name, s.inputs, s.outputs, len(chars))
		s.close()
	}
}
