package app

import (
	"strings"
	"testing"
)

func TestExtractImageRefs(t *testing.T) {
	content := "# Note\n\n![image](./images/img-20260101.png)\n\nSome text\n\n![another](./images/photo.jpg)\n\nNot an image: [link](http://example.com)"
	refs := extractImageRefs(content)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "img-20260101.png" {
		t.Errorf("expected img-20260101.png, got %q", refs[0])
	}
	if refs[1] != "photo.jpg" {
		t.Errorf("expected photo.jpg, got %q", refs[1])
	}
}

func TestRenderImagesInMarkdown(t *testing.T) {
	content := "# Title\n\n![my image](./images/test.png)\n\nParagraph"
	rendered := renderImagesInMarkdown(content)
	if !strings.Contains(rendered, "[📷 my image: test.png]") {
		t.Errorf("expected placeholder, got %q", rendered)
	}
	if strings.Contains(rendered, "![my image]") {
		t.Errorf("original image syntax should be replaced, got %q", rendered)
	}
}

func TestSniffExt(t *testing.T) {
	tests := []struct {
		data []byte
		want string
	}{
		{[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "png"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "jpg"},
		{[]byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, "gif"},
		{[]byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, "gif"},
		{[]byte{0x52, 0x49, 0x46, 0x46}, "webp"},
		{[]byte("unknown"), "png"},
	}
	for _, tt := range tests {
		got := sniffExt(tt.data)
		if got != tt.want {
			t.Errorf("sniffExt(%x) = %q, want %q", tt.data, got, tt.want)
		}
	}
}

func TestImagePlaceholder(t *testing.T) {
	got := imagePlaceholder("alt text", "./images/test.png")
	want := "[📷 alt text: test.png]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = imagePlaceholder("", "./images/test.png")
	want = "[📷 test.png]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
