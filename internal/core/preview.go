package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/pkg/sftp"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const previewPageBytes = 128 * 1024
const previewSampleBytes = 4096
const thumbnailSide = 192
const thumbnailPixelLimit = 32 * 1024 * 1024

var thumbnailSlots = make(chan struct{}, 2)

type textEncoding struct {
	name  string
	bom   []byte
	order binary.ByteOrder
}

func detectEncoding(data []byte) textEncoding {
	switch {
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		return textEncoding{name: "UTF-8 BOM", bom: []byte{0xef, 0xbb, 0xbf}}
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		return textEncoding{name: "UTF-16 LE", bom: []byte{0xff, 0xfe}, order: binary.LittleEndian}
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		return textEncoding{name: "UTF-16 BE", bom: []byte{0xfe, 0xff}, order: binary.BigEndian}
	default:
		return textEncoding{name: "UTF-8"}
	}
}

func (e textEncoding) decode(data []byte, first bool) (string, error) {
	const invalidText = "文件不是有效的 UTF-8 或带 BOM 的 UTF-16 文本，请下载查看"
	if first {
		data = bytes.TrimPrefix(data, e.bom)
	}
	if e.order == nil {
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", errors.New(invalidText)
		}
		return string(data), nil
	}
	if len(data)%2 != 0 {
		return "", errors.New(invalidText)
	}
	var text strings.Builder
	for len(data) > 0 {
		r := rune(e.order.Uint16(data))
		data = data[2:]
		if utf16.IsSurrogate(r) {
			if len(data) < 2 || r < 0xd800 || r > 0xdbff {
				return "", errors.New(invalidText)
			}
			next := rune(e.order.Uint16(data))
			if next < 0xdc00 || next > 0xdfff {
				return "", errors.New(invalidText)
			}
			r = utf16.DecodeRune(r, next)
			data = data[2:]
		}
		if r == 0 {
			return "", errors.New(invalidText)
		}
		text.WriteRune(r)
	}
	return text.String(), nil
}

func (e textEncoding) encode(content string) []byte {
	if e.order == nil {
		data := make([]byte, 0, len(e.bom)+len(content))
		data = append(data, e.bom...)
		return append(data, content...)
	}
	units := utf16.Encode([]rune(content))
	data := make([]byte, len(e.bom)+len(units)*2)
	copy(data, e.bom)
	for i, unit := range units {
		e.order.PutUint16(data[len(e.bom)+i*2:], unit)
	}
	return data
}

func imageMIME(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{137, 80, 78, 71, 13, 10, 26, 10}):
		return "image/png"
	case bytes.HasPrefix(data, []byte{255, 216, 255}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 14 && bytes.HasPrefix(data, []byte("BM")) && binary.LittleEndian.Uint32(data[6:10]) == 0:
		return "image/bmp"
	default:
		return ""
	}
}

func readPreviewRange(r io.ReaderAt, offset, count int64) ([]byte, error) {
	data := make([]byte, int(count))
	if count == 0 {
		return data, nil
	}
	n, err := r.ReadAt(data, offset)
	if int64(n) == count && (err == nil || errors.Is(err, io.EOF)) {
		return data, nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return nil, errors.New("远程文件大小已变化，请重新加载")
	}
	return nil, err
}

func (f *Files) Preview(id, remote string) (Preview, error) {
	return f.readPreview(id, remote, 0, "")
}

func (f *Files) PreviewPage(id, remote string, offset int64, version string) (Preview, error) {
	if offset < 0 || len(version) != 64 {
		return Preview{}, errors.New("无效的预览页位置或版本")
	}
	return f.readPreview(id, remote, offset, version)
}

func (f *Files) readPreview(id, remote string, offset int64, expectedVersion string) (Preview, error) {
	c, err := f.client(id, remote)
	if err != nil {
		return Preview{}, err
	}
	info, err := c.Stat(remote)
	if err != nil {
		return Preview{}, err
	}
	if !info.Mode().IsRegular() {
		return Preview{}, errors.New("只能预览普通文件")
	}
	if info.Size() < 0 || info.Size() > 1<<53-1 {
		return Preview{}, errors.New("文件大小无法安全预览，请下载查看")
	}
	r, err := c.Open(remote)
	if err != nil {
		return Preview{}, err
	}
	defer r.Close()
	sample, err := readPreviewRange(r, 0, min(info.Size(), previewSampleBytes))
	if err != nil {
		return Preview{}, err
	}
	result := Preview{Path: remote, Size: info.Size(), Modified: info.ModTime().UnixMilli()}
	mime := imageMIME(sample)
	if mime == "" && info.Size() > TextLimit {
		result, err = readLargeTextPage(r, result, sample, offset, expectedVersion)
		if err != nil {
			return Preview{}, err
		}
		if err = verifyPreviewFile(c, remote, info.Size(), result.Modified); err != nil {
			return Preview{}, err
		}
		return result, nil
	}
	if expectedVersion != "" {
		return Preview{}, errors.New("CONFLICT:远程文件已变化，请重新加载")
	}
	if mime != "" && info.Size() > ImageLimit {
		return Preview{}, errors.New("图片超过 20 MiB 预览限制，请下载查看")
	}
	data := sample
	if int64(len(sample)) < info.Size() {
		data, err = readPreviewRange(r, 0, info.Size())
		if err != nil {
			return Preview{}, err
		}
	}
	if err = verifyPreviewFile(c, remote, info.Size(), result.Modified); err != nil {
		return Preview{}, err
	}
	result.Version = contentVersion(data)
	if mime != "" {
		result.Kind, result.Mime = "image", mime
		result.Content = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return result, nil
	}
	encoding := detectEncoding(data)
	result.Kind, result.Encoding = "text", encoding.name
	result.Content, err = encoding.decode(data, true)
	if err != nil {
		return Preview{}, err
	}
	return result, nil
}

func verifyPreviewFile(c *sftp.Client, remote string, size, modified int64) error {
	info, err := c.Stat(remote)
	if err != nil {
		return err
	}
	if info.Size() != size || info.ModTime().UnixMilli() != modified {
		return errors.New("CONFLICT:远程文件已变化，请重新加载")
	}
	return nil
}

func readLargeTextPage(r io.ReaderAt, result Preview, sample []byte, offset int64, expectedVersion string) (Preview, error) {
	if offset >= result.Size {
		return Preview{}, errors.New("预览页超出文件范围")
	}
	// A large-file version uses metadata and a bounded prefix. Reading pages
	// must never hash or download the whole file, and pages are read-only.
	hash := sha256.New()
	fmt.Fprintf(hash, "paged:%d:%d:", result.Size, result.Modified)
	hash.Write(sample)
	result.Version = hex.EncodeToString(hash.Sum(nil))
	if expectedVersion != "" && result.Version != expectedVersion {
		return Preview{}, errors.New("CONFLICT:远程文件已变化，请重新加载")
	}
	encoding := detectEncoding(sample)
	if encoding.order != nil && offset%2 != 0 {
		return Preview{}, errors.New("UTF-16 预览页未对齐")
	}
	remaining := result.Size - offset
	data, err := readPreviewRange(r, offset, min(remaining, previewPageBytes+4))
	if err != nil {
		return Preview{}, err
	}
	end := min(len(data), previewPageBytes)
	if end < len(data) {
		if encoding.order == nil {
			// The next page starts at the beginning of a complete rune.
			for end > 0 && !utf8.RuneStart(data[end]) {
				end--
			}
		} else {
			last := encoding.order.Uint16(data[end-2 : end])
			if last >= 0xd800 && last <= 0xdbff {
				end -= 2
			}
		}
	}
	if end == 0 {
		return Preview{}, errors.New("无法找到有效文本的分页边界")
	}
	result.Content, err = encoding.decode(data[:end], offset == 0)
	if err != nil {
		return Preview{}, err
	}
	result.Kind, result.Encoding = "text", encoding.name
	result.Page = &PreviewPage{Offset: offset}
	if offset+int64(end) < result.Size {
		result.Page.NextOffset = offset + int64(end)
	}
	return result, nil
}

func (f *Files) Thumbnail(id, remote string) (string, error) {
	c, err := f.client(id, remote)
	if err != nil {
		return "", err
	}
	select {
	case thumbnailSlots <- struct{}{}:
		defer func() { <-thumbnailSlots }()
	default:
		return "", errors.New("缩略图读取繁忙，请重试")
	}
	data, _, err := boundedRead(c, remote, ImageLimit)
	if err != nil {
		return "", err
	}
	return encodeThumbnail(data)
}

func encodeThumbnail(data []byte) (string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > thumbnailPixelLimit/config.Height {
		return "", errors.New("无法安全生成图片缩略图")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("无法解码缩略图")
	}
	width, height := config.Width, config.Height
	if longest := max(width, height); longest > thumbnailSide {
		width, height = max(1, width*thumbnailSide/longest), max(1, height*thumbnailSide/longest)
	}
	preview := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(preview, preview.Bounds(), img, img.Bounds(), draw.Src, nil)
	var output bytes.Buffer
	if err = jpeg.Encode(&output, preview, &jpeg.Options{Quality: 78}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}
