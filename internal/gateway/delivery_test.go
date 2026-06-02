package gateway

import (
	"strings"
	"testing"

	"nexus-agent/internal/gateway/platforms"
)

// ---------------------------------------------------------------------------
// NewDeliveryManager
// ---------------------------------------------------------------------------

func TestNewDeliveryManager(t *testing.T) {
	t.Parallel()

	t.Run("positive truncateLen", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(2048)
		if dm.truncateLen != 2048 {
			t.Errorf("truncateLen = %d, want 2048", dm.truncateLen)
		}
	})

	t.Run("zero truncateLen defaults to 4096", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(0)
		if dm.truncateLen != 4096 {
			t.Errorf("truncateLen = %d, want 4096", dm.truncateLen)
		}
	})

	t.Run("negative truncateLen defaults to 4096", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(-10)
		if dm.truncateLen != 4096 {
			t.Errorf("truncateLen = %d, want 4096", dm.truncateLen)
		}
	})
}

// ---------------------------------------------------------------------------
// FormatMessage
// ---------------------------------------------------------------------------

func TestFormatMessage(t *testing.T) {
	t.Parallel()

	dm := NewDeliveryManager(4096)
	content := "hello **world**"

	tests := []struct {
		name     string
		platform platforms.Platform
	}{
		{"telegram", platforms.PlatformTelegram},
		{"slack", platforms.PlatformSlack},
		{"discord", platforms.PlatformDiscord},
		{"unknown", platforms.Platform("unknown")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dm.FormatMessage(content, tc.platform)
			if got != content {
				t.Errorf("FormatMessage(%q, %v) = %q, want %q", content, tc.platform, got, content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DeliveryManager.TruncateMessage
// ---------------------------------------------------------------------------

func TestDeliveryManagerTruncateMessage(t *testing.T) {
	t.Parallel()

	t.Run("short text unchanged", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		got := dm.TruncateMessage("hello", 100)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		got := dm.TruncateMessage("hello", 0)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("truncates at paragraph boundary", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := "first paragraph\n\nsecond paragraph that is here"
		got := dm.TruncateMessage(text, 20)
		if len([]rune(got)) > 20 {
			t.Errorf("result too long: %d chars: %q", len([]rune(got)), got)
		}
	})

	t.Run("truncates at line boundary", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := "line1\nline2\nline3\nline4"
		got := dm.TruncateMessage(text, 15)
		if len([]rune(got)) > 15 {
			t.Errorf("result too long: %d chars: %q", len([]rune(got)), got)
		}
	})

	t.Run("very small maxLen hard truncates", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := "hello world this is long"
		got := dm.TruncateMessage(text, 8)
		if len([]rune(got)) > 8 {
			t.Errorf("result too long: %d chars: %q", len([]rune(got)), got)
		}
	})

	t.Run("exact length not truncated", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := "hello"
		got := dm.TruncateMessage(text, 5)
		if got != text {
			t.Errorf("got %q, want %q", got, text)
		}
	})
}

// ---------------------------------------------------------------------------
// SplitLongMessage
// ---------------------------------------------------------------------------

func TestSplitLongMessage(t *testing.T) {
	t.Parallel()

	t.Run("short text returns single chunk", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		got := dm.SplitLongMessage("hello", 100)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("zero maxLen uses default", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		got := dm.SplitLongMessage("hello", 0)
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [hello]", got)
		}
	})

	t.Run("splits at paragraph boundaries", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := "first paragraph\n\nsecond paragraph\n\nthird paragraph"
		got := dm.SplitLongMessage(text, 20)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks, got %d: %v", len(got), got)
		}
	})

	t.Run("long paragraph split by lines", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		longPara := strings.Repeat("line\n", 50)
		got := dm.SplitLongMessage(longPara, 30)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks for long paragraph, got %d", len(got))
		}
	})

	t.Run("single long line hard truncation fallback", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		text := strings.Repeat("a", 100)
		got := dm.SplitLongMessage(text, 30)
		if len(got) < 2 {
			t.Errorf("expected multiple chunks, got %d", len(got))
		}
	})

	t.Run("empty string returns single chunk", func(t *testing.T) {
		t.Parallel()
		dm := NewDeliveryManager(4096)
		got := dm.SplitLongMessage("", 100)
		if len(got) != 1 {
			t.Errorf("expected 1 chunk, got %d", len(got))
		}
	})
}

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

// ---------------------------------------------------------------------------
// contains
// ---------------------------------------------------------------------------

func TestContains(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		if !contains([]string{"a", "b", "c"}, "b") {
			t.Error("expected true")
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		if contains([]string{"a", "b"}, "c") {
			t.Error("expected false")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()
		if contains(nil, "a") {
			t.Error("expected false for nil slice")
		}
	})
}
