// Package logic 业务逻辑层：编排 DAO 与工具函数，实现用户模块业务规则。
// 本文件为头像上传：文件校验（大小/魔数）→ 本地磁盘落盘 → 更新 users.avatar 列。
package logic

import (
	"bytes"
	"campuscommunity/internal/conf"
	"campuscommunity/internal/dao"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// 头像上传哨兵错误：controller 用 errors.Is 识别并映射为业务响应码。
var (
	ErrAvatarFormat   = errors.New("头像格式不支持")
	ErrAvatarTooLarge = errors.New("头像文件过大")
)

// avatarExts 允许的图片扩展名白名单（兼作旧文件清理的枚举范围）。
var avatarExts = []string{"jpg", "png", "webp"}

// UploadAvatar 头像上传：校验（大小/魔数）→ 落盘 {dir}/avatars/{user_id}.{ext}
// → 清理旧格式残留 → 更新 avatar 列 → 返回相对 URL。
// userID 只来自 JWT（只能改自己的头像）；文件名由服务端生成，防路径遍历。
// 写盘成功但 DB 失败时返回错误由客户端重传——孤儿文件被下次覆盖，无需补偿。
func UploadAvatar(userID int64, fh *multipart.FileHeader) (string, error) {
	// 大小校验：配置驱动（MB → 字节）
	maxSize := conf.Conf.UploadConfig.MaxSizeMB * 1024 * 1024
	if fh.Size > maxSize {
		return "", ErrAvatarTooLarge
	}

	// 魔数嗅探：读头部字节判定真实格式，不信任 filename/Content-Type
	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("logic: open avatar upload: %w", err)
	}
	defer src.Close()
	buf := make([]byte, 512)
	n, err := src.Read(buf)
	if err != nil && n == 0 {
		return "", fmt.Errorf("logic: read avatar head: %w", err)
	}
	ext, err := sniffImageExt(buf[:n])
	if err != nil {
		return "", ErrAvatarFormat
	}
	// 嗅探后读指针拨回文件头，落盘写完整文件
	if _, err := src.Seek(0, 0); err != nil {
		return "", fmt.Errorf("logic: seek avatar head: %w", err)
	}

	// 落盘：MkdirAll 幂等（目录被人工删除时上传自愈）
	avatarDir := filepath.Join(conf.Conf.UploadConfig.Dir, "avatars")
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return "", fmt.Errorf("logic: mkdir avatar dir: %w", err)
	}
	dstPath := filepath.Join(avatarDir, fmt.Sprintf("%d%s", userID, ext))
	dst, err := os.Create(dstPath)
	if err != nil {
		return "", fmt.Errorf("logic: create avatar file: %w", err)
	}
	if err := writeAvatar(dst, src, ext); err != nil {
		dst.Close()
		os.Remove(dstPath) // 写一半失败：清残缺文件
		return "", fmt.Errorf("logic: write avatar file: %w", err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(dstPath)
		return "", fmt.Errorf("logic: close avatar file: %w", err)
	}

	cleanStaleAvatars(avatarDir, userID, ext)

	relURL := "/uploads/avatars/" + fmt.Sprintf("%d%s", userID, ext)
	if err := dao.UpdateUserProfile(userID, map[string]any{"avatar": relURL}); err != nil {
		return "", fmt.Errorf("logic: update user avatar: %w", err)
	}
	zap.L().Info("logic: avatar uploaded",
		zap.Int64("user_id", userID), zap.String("ext", ext), zap.Int64("size", fh.Size))
	return relURL, nil
}

// writeAvatar 落盘写入：jpg/png 解码重编码（验证完整可解码并剥离元数据），
// webp 标准库无解码器，原样拷贝（格式已经魔数验证）。
func writeAvatar(dst *os.File, src multipart.File, ext string) error {
	switch ext {
	case ".jpg":
		img, err := jpeg.Decode(src)
		if err != nil {
			return fmt.Errorf("logic: decode avatar jpeg: %w", err)
		}
		if err := jpeg.Encode(dst, img, nil); err != nil {
			return fmt.Errorf("logic: encode avatar jpeg: %w", err)
		}
	case ".png":
		img, err := png.Decode(src)
		if err != nil {
			return fmt.Errorf("logic: decode avatar png: %w", err)
		}
		if err := png.Encode(dst, img); err != nil {
			return fmt.Errorf("logic: encode avatar png: %w", err)
		}
	default: // .webp
		if _, err := io.Copy(dst, src); err != nil {
			return fmt.Errorf("logic: copy avatar webp: %w", err)
		}
	}
	return nil
}

// sniffImageExt 按文件头字节判定图片真实格式，返回带点扩展名；
// 白名单外返回 ErrAvatarFormat。
// 魔数：JPEG = FF D8 FF；PNG = 89 50 4E 47 0D 0A 1A 0A；WEBP = RIFF????WEBP。
func sniffImageExt(head []byte) (string, error) {
	switch {
	case len(head) >= 3 && bytes.Equal(head[:3], []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg", nil
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return ".png", nil
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return ".webp", nil
	default:
		return "", ErrAvatarFormat
	}
}

// cleanStaleAvatars 删除该用户旧格式头像文件（换格式上传后旧文件成死文件）；
// IsNotExist 为常态静默，真删除失败仅记日志（残留不影响正确性，DB 指向新 URL）。
func cleanStaleAvatars(avatarDir string, userID int64, curExt string) {
	for _, ext := range avatarExts {
		e := "." + strings.TrimPrefix(ext, ".")
		if e == curExt {
			continue
		}
		old := filepath.Join(avatarDir, fmt.Sprintf("%d%s", userID, e))
		if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
			zap.L().Warn("logic: remove stale avatar failed",
				zap.Int64("user_id", userID), zap.String("path", old), zap.Error(err))
		}
	}
}
