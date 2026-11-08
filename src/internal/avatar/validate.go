package avatar

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// kind 是服务端依据真实魔数判定出的图片类型，ext 只来自白名单常量。
type kind struct {
	ext         string
	contentType string
	// decodable 表示标准库 image 能解码（png/jpeg/gif）；webp 为 false。
	decodable bool
}

var (
	// PNG 89 50 4E 47 0D 0A 1A 0A
	pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	// JPEG FF D8 FF
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	// WebP RIFF .... WEBP
	webpRIFF = []byte("RIFF")
	webpWEBP = []byte("WEBP")
	// GIF 47 49 46 38（"GIF8"），后两字节为版本 7a/9a。
	gifMagic = []byte{0x47, 0x49, 0x46, 0x38}
)

var allowedContentTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true,
}

// sniff 只依据文件真实魔数判定类型；用户文件名与 Content-Type 都不参与判定。
func sniff(data []byte) (kind, bool) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], pngMagic):
		return kind{ext: ".png", contentType: "image/png", decodable: true}, true
	case len(data) >= 3 && bytes.Equal(data[:3], jpegMagic):
		return kind{ext: ".jpg", contentType: "image/jpeg", decodable: true}, true
	case len(data) >= 12 && bytes.Equal(data[:4], webpRIFF) && bytes.Equal(data[8:12], webpWEBP):
		// WebP 容器：标准库无解码器，只按魔数校验，原样保存并标记。
		return kind{ext: ".webp", contentType: "image/webp", decodable: false}, true
	case len(data) >= 6 && bytes.Equal(data[:4], gifMagic) &&
		(bytes.Equal(data[4:6], []byte("7a")) || bytes.Equal(data[4:6], []byte("9a"))):
		return kind{ext: ".gif", contentType: "image/gif", decodable: true}, true
	default:
		return kind{}, false
	}
}

// readAvatarPart 流式读取 multipart 中名为 avatar 的字段。
// part.FileName()（用户上传的原始文件名）被刻意忽略，绝不用于路径、响应或日志，
// 从而同时防住路径穿越与通过文件名注入的 XSS。
func readAvatarPart(r *http.Request) (data []byte, declaredCT string, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, "", fmt.Errorf("请求必须是 multipart/form-data 并含 avatar 字段: %w", err)
	}
	for {
		part, nerr := mr.NextPart()
		if nerr == io.EOF {
			return nil, "", fmt.Errorf("缺少 multipart 字段 avatar")
		}
		if nerr != nil {
			return nil, "", fmt.Errorf("读取 multipart 失败: %w", nerr)
		}
		if part.FormName() != "avatar" {
			_ = part.Close()
			continue
		}
		declaredCT = part.Header.Get("Content-Type")
		// 多读 1 字节用于判定是否超限，避免把超大文件一次性读进内存后再拒绝。
		data, nerr = io.ReadAll(io.LimitReader(part, maxAvatarBytes+1))
		_ = part.Close()
		if nerr != nil {
			return nil, "", fmt.Errorf("读取头像数据失败: %w", nerr)
		}
		return data, declaredCT, nil
	}
}

// validateAvatar 校验大小、真实魔数、Content-Type 三者一致。
func validateAvatar(data []byte, declaredCT string) (kind, error) {
	if len(data) == 0 {
		return kind{}, fmt.Errorf("头像文件为空")
	}
	if len(data) > maxAvatarBytes {
		return kind{}, fmt.Errorf("头像超过 2MiB 上限（%d bytes）", len(data))
	}
	k, ok := sniff(data)
	if !ok {
		return kind{}, fmt.Errorf("文件魔数不匹配 PNG(89 50 4E 47)/JPEG(FF D8 FF)/WebP(RIFF....WEBP)/GIF(47 49 46 38)")
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(declaredCT, ";")[0]))
	if !allowedContentTypes[ct] {
		return kind{}, fmt.Errorf("Content-Type %q 不在允许列表 image/png|jpeg|webp|gif", declaredCT)
	}
	if ct != k.contentType {
		return kind{}, fmt.Errorf("Content-Type %q 与真实文件类型 %q 不一致", declaredCT, k.contentType)
	}
	return k, nil
}
