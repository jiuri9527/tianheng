package avatar

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // 注册 GIF 解码器（标准库，零第三方依赖）
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
)

// maxDecodePixels 限制解码后像素总量，防止 2MiB 的压缩炸弹解码成超大位图耗尽内存。
const maxDecodePixels int64 = 25_000_000 // 25 MP

var expectedDecodeFormat = map[string]string{
	".png": "png", ".jpg": "jpeg", ".gif": "gif",
}

// processImage 对 PNG/JPEG/GIF 做真实解码校验并生成 128×128 缩略图；
// WebP 标准库无解码器，返回 (nil, false, nil) 表示「仅魔数校验、原样保存、不生成缩略图」。
func (s *Service) processImage(k kind, data []byte) (thumb []byte, decoded bool, err error) {
	if !k.decodable {
		return nil, false, nil
	}
	// 先读配置，校验格式与像素总量，再真正解码。
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("图片配置解析失败（不是有效 %s）: %w", k.contentType, err)
	}
	if want, ok := expectedDecodeFormat[k.ext]; ok && format != want {
		return nil, false, fmt.Errorf("图片内部格式 %q 与魔数判定的 %q 不一致", format, want)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return nil, false, fmt.Errorf("图片尺寸 %dx%d 超出安全上限（%d 像素）", cfg.Width, cfg.Height, maxDecodePixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("图片解码失败（不是有效 %s）: %w", k.contentType, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, resizeBox(img, thumbSize)); err != nil {
		return nil, false, fmt.Errorf("缩略图编码失败: %w", err)
	}
	return buf.Bytes(), true, nil
}

// resizeBox 用「中心正方形裁剪 + 盒式均值降采样」生成 size×size 缩略图。
// 纯标准库实现（无 golang.org/x/image/draw），保持零第三方 Go 依赖。
func resizeBox(src image.Image, size int) *image.RGBA {
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	if side < 1 {
		side = 1
	}
	ox := b.Min.X + (b.Dx()-side)/2
	oy := b.Min.Y + (b.Dy()-side)/2
	crop := image.Rect(ox, oy, ox+side, oy+side)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		y0 := crop.Min.Y + y*side/size
		y1 := crop.Min.Y + (y+1)*side/size
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > crop.Max.Y {
			y1 = crop.Max.Y
		}
		for x := 0; x < size; x++ {
			x0 := crop.Min.X + x*side/size
			x1 := crop.Min.X + (x+1)*side/size
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > crop.Max.X {
				x1 = crop.Max.X
			}
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					rr, gg, bb, aa := src.At(sx, sy).RGBA() // 16-bit 预乘
					r, g, bl, a = r+uint64(rr), g+uint64(gg), bl+uint64(bb), a+uint64(aa)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((r / n) >> 8), G: uint8((g / n) >> 8),
				B: uint8((bl / n) >> 8), A: uint8((a / n) >> 8),
			})
		}
	}
	return dst
}

// saveAvatarFiles 在锁内原子写原图与缩略图，并清理上一次上传留下的其它扩展名文件。
func (s *Service) saveAvatarFiles(userID string, k kind, data, thumb []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ext := range imageExts {
		if ext == k.ext {
			continue
		}
		if p, ok := s.avatarPath(userID, ext); ok {
			_ = os.Remove(p)
		}
	}
	tp, ok := s.avatarPath(userID, thumbExt)
	if !ok {
		return fmt.Errorf("非法缩略图路径")
	}
	if len(thumb) == 0 {
		// WebP：无缩略图，删除可能存在的旧缩略图。
		_ = os.Remove(tp)
	} else if err := atomicWrite(tp, thumb, 0o600); err != nil {
		return fmt.Errorf("写缩略图失败: %w", err)
	}
	op, ok := s.avatarPath(userID, k.ext)
	if !ok {
		return fmt.Errorf("非法头像路径")
	}
	if err := atomicWrite(op, data, 0o600); err != nil {
		return fmt.Errorf("写原图失败: %w", err)
	}
	return nil
}

// atomicWrite 同目录临时文件 + fsync + rename + chmod，保证 0600 且不产生半截文件。
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
