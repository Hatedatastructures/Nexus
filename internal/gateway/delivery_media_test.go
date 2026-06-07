package gateway

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ExtractMedia
// ---------------------------------------------------------------------------

func TestExtractMedia(t *testing.T) {
	t.Parallel()

	dm := NewDeliveryManager(4096)

	t.Run("plain text no media", func(t *testing.T) {
		t.Parallel()
		text, images, tags := dm.ExtractMedia("hello world")
		if text != "hello world" {
			t.Errorf("text = %q, want %q", text, "hello world")
		}
		if len(images) != 0 {
			t.Errorf("images = %v, want empty", images)
		}
		if len(tags) != 0 {
			t.Errorf("tags = %v, want empty", tags)
		}
	})

	t.Run("extracts MEDIA tag", func(t *testing.T) {
		t.Parallel()
		text, _, tags := dm.ExtractMedia("MEDIA: photo.jpg\nsome text")
		if len(tags) != 1 {
			t.Fatalf("expected 1 tag, got %d", len(tags))
		}
		if tags[0].Type != "image" {
			t.Errorf("tag type = %q, want %q", tags[0].Type, "image")
		}
		if tags[0].URL != "photo.jpg" {
			t.Errorf("tag URL = %q, want %q", tags[0].URL, "photo.jpg")
		}
		if text != "some text" {
			t.Errorf("text = %q, want %q", text, "some text")
		}
	})

	t.Run("extracts [[MEDIA:...]] tag", func(t *testing.T) {
		t.Parallel()
		text, _, tags := dm.ExtractMedia("[[MEDIA:image.png]]")
		if len(tags) != 1 {
			t.Fatalf("expected 1 tag, got %d", len(tags))
		}
		if tags[0].URL != "image.png" {
			t.Errorf("tag URL = %q, want %q", tags[0].URL, "image.png")
		}
		if strings.Contains(text, "[[MEDIA:") {
			t.Errorf("tag should be removed from text, got %q", text)
		}
	})

	t.Run("extracts markdown image", func(t *testing.T) {
		t.Parallel()
		_, images, _ := dm.ExtractMedia("see ![alt](https://example.com/photo.jpg)")
		if len(images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(images))
		}
		if images[0] != "https://example.com/photo.jpg" {
			t.Errorf("image = %q, want %q", images[0], "https://example.com/photo.jpg")
		}
	})

	t.Run("extracts inline image URL", func(t *testing.T) {
		t.Parallel()
		_, images, _ := dm.ExtractMedia("check this https://example.com/cat.png ok")
		if len(images) != 1 {
			t.Fatalf("expected 1 image, got %d", len(images))
		}
		if images[0] != "https://example.com/cat.png" {
			t.Errorf("image = %q, want %q", images[0], "https://example.com/cat.png")
		}
	})

	t.Run("no duplicate images", func(t *testing.T) {
		t.Parallel()
		_, images, _ := dm.ExtractMedia("![a](https://x.com/a.jpg) and https://x.com/a.jpg")
		count := 0
		for _, img := range images {
			if img == "https://x.com/a.jpg" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected 1 unique image, got %d occurrences", count)
		}
	})

	t.Run("cleans multiple newlines", func(t *testing.T) {
		t.Parallel()
		text, _, _ := dm.ExtractMedia("hello\n\n\n\nworld")
		if strings.Contains(text, "\n\n\n") {
			t.Error("expected multiple newlines to be cleaned")
		}
	})

	t.Run("detects audio media type", func(t *testing.T) {
		t.Parallel()
		_, _, tags := dm.ExtractMedia("MEDIA: song.mp3")
		if len(tags) != 1 {
			t.Fatalf("expected 1 tag, got %d", len(tags))
		}
		if tags[0].Type != "audio" {
			t.Errorf("type = %q, want %q", tags[0].Type, "audio")
		}
	})

	t.Run("detects file media type", func(t *testing.T) {
		t.Parallel()
		_, _, tags := dm.ExtractMedia("MEDIA: document.pdf")
		if len(tags) != 1 {
			t.Fatalf("expected 1 tag, got %d", len(tags))
		}
		if tags[0].Type != "file" {
			t.Errorf("type = %q, want %q", tags[0].Type, "file")
		}
	})
}

// ---------------------------------------------------------------------------
// isImageURL
// ---------------------------------------------------------------------------

func TestIsImageURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url  string
		want bool
	}{
		{"photo.jpg", true},
		{"photo.jpeg", true},
		{"photo.PNG", true},
		{"photo.gif", true},
		{"photo.webp", true},
		{"photo.bmp", false},
		{"photo.txt", false},
		{"noextension", false},
	}

	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()
			got := isImageURL(tc.url)
			if got != tc.want {
				t.Errorf("isImageURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectMediaType
// ---------------------------------------------------------------------------

func TestDetectMediaType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{"photo.jpg", "image"},
		{"photo.jpeg", "image"},
		{"photo.png", "image"},
		{"photo.gif", "image"},
		{"photo.webp", "image"},
		{"song.mp3", "audio"},
		{"song.wav", "audio"},
		{"song.ogg", "audio"},
		{"doc.pdf", "file"},
		{"data.json", "file"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got := detectMediaType(tc.path)
			if got != tc.want {
				t.Errorf("detectMediaType(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
