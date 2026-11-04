package storage

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// secretPrefix 标记加密过的字段，便于和旧的明文值区分。
const secretPrefix = "dpapi:"

// EncryptSecret 用 Windows DPAPI 加密一段机密（如 API Key），返回可写进 JSON 的文本。
// 只有同一 Windows 用户能解开，拷到别的电脑或别的账号下读不出来。空字符串原样返回。
func EncryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	in := []byte(plain)
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("加密失败：%w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return secretPrefix + base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

// DecryptSecret 解开 EncryptSecret 的结果。没有加密前缀的值当作明文直接返回（兼容手工编辑的配置）。
func DecryptSecret(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if len(enc) < len(secretPrefix) || enc[:len(secretPrefix)] != secretPrefix {
		return enc, nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc[len(secretPrefix):])
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("密文格式不对")
	}
	inBlob := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("解密失败（可能是换了电脑或 Windows 账号）：%w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}
