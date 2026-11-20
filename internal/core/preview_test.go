package core

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestPreviewDetectsEncodingAndPreservesItOnSave(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	tests := []struct {
		name, encoding  string
		original, saved []byte
	}{
		{"utf8-bom.txt", "UTF-8 BOM", []byte{0xef, 0xbb, 0xbf, 'a'}, []byte{0xef, 0xbb, 0xbf, 'b'}},
		{"le.txt", "UTF-16 LE", []byte{0xff, 0xfe, 'a', 0}, []byte{0xff, 0xfe, 'b', 0}},
		{"be.txt", "UTF-16 BE", []byte{0xfe, 0xff, 0, 'a'}, []byte{0xfe, 0xff, 0, 'b'}},
	}
	for _, test := range tests {
		remote := "/home/test/" + test.name
		fileWrite(t, f.sftp, remote, test.original)
		preview, err := files.Preview("session", remote)
		if err != nil || preview.Encoding != test.encoding || preview.Content != "a" {
			t.Fatalf("wrong encoding for %s: %+v %v", test.name, preview, err)
		}
		updated, err := files.Save("session", remote, "b", preview.Version)
		if err != nil || updated.Content != "b" || !bytes.Equal(fileRead(t, f.sftp, remote), test.saved) {
			t.Fatalf("save did not preserve %s: %+v %v", test.encoding, updated, err)
		}
	}
}

func TestLargePreviewReadsPagesWithoutSplittingUnicode(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	const remote = "/home/test/large.txt"
	data := []byte(strings.Repeat("a", previewPageBytes-1) + "🙂" + strings.Repeat("b", TextLimit))
	fileWrite(t, f.sftp, remote, data)
	first, err := files.Preview("session", remote)
	if err != nil || first.Page == nil || first.Page.Offset != 0 || first.Page.NextOffset != previewPageBytes-1 || len(first.Content) != previewPageBytes-1 {
		t.Fatalf("wrong first page: %+v %v", first.Page, err)
	}
	second, err := files.PreviewPage("session", remote, first.Page.NextOffset, first.Version)
	if err != nil || second.Page == nil || !strings.HasPrefix(second.Content, "🙂b") || len(second.Content) > previewPageBytes {
		t.Fatalf("wrong second page: %+v %v", second.Page, err)
	}
	w, err := f.sftp.OpenFile(remote, os.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.WriteAt([]byte{'z'}, 0); err != nil {
		w.Close()
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = files.PreviewPage("session", remote, first.Page.NextOffset, first.Version); err == nil || !strings.HasPrefix(err.Error(), "CONFLICT:") {
		t.Fatalf("changed large file was not detected: %v", err)
	}
}

func TestLargeUTF16PreviewAlignsSurrogatePairs(t *testing.T) {
	encoding := detectEncoding([]byte{0xfe, 0xff})
	data := encoding.encode(strings.Repeat("a", previewPageBytes/2-2) + "🙂" + strings.Repeat("b", TextLimit/2))
	info := Preview{Size: int64(len(data)), Modified: 100}
	sample := data[:previewSampleBytes]
	first, err := readLargeTextPage(bytes.NewReader(data), info, sample, 0, "")
	if err != nil || first.Page == nil || first.Page.NextOffset != previewPageBytes-2 || first.Encoding != "UTF-16 BE" {
		t.Fatalf("wrong first UTF-16 page: %+v %v", first.Page, err)
	}
	next, err := readLargeTextPage(bytes.NewReader(data), info, sample, first.Page.NextOffset, first.Version)
	if err != nil || !strings.HasPrefix(next.Content, "🙂b") {
		t.Fatalf("split UTF-16 surrogate pair: %v", err)
	}
}

func TestThumbnailUsesBoundedImageDimensions(t *testing.T) {
	f := newFileFixture(t)
	files := NewFiles(f)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 500, 250))); err != nil {
		t.Fatal(err)
	}
	const remote = "/home/test/images/sample.png"
	fileWrite(t, f.sftp, remote, data.Bytes())
	thumb, err := files.Thumbnail("session", remote)
	if err != nil || !strings.HasPrefix(thumb, "data:image/jpeg;base64,") {
		t.Fatalf("cannot read thumbnail: %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(thumb, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(encoded))
	if err != nil || config.Width != 192 || config.Height != 96 {
		t.Fatalf("unexpected thumbnail dimensions: %+v %v", config, err)
	}
}
