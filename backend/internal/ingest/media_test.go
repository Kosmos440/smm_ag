package ingest

import (
	"net/url"
	"testing"
)

func TestExtractMedia(t *testing.T) {
	base, _ := url.Parse("https://example.org/news/one")
	for _, tc := range []struct{ raw, kind, path string }{
		{`<article><img src="/photo.jpg"></article>`, "photo", "/photo.jpg"},
		{`<a class="tgme_widget_message_photo_wrap" style="background-image:url('https://example.org/tg.jpg')"></a>`, "photo", "/tg.jpg"},
		{`<video src="/clip.mp4"></video><img src="/poster.jpg">`, "video", "/clip.mp4"},
		{`<meta property="og:image" content="/cover.jpg">`, "photo", "/cover.jpg"},
	} {
		doc, err := ParseHTML(Page{URL: base, Body: []byte(tc.raw)})
		if err != nil {
			t.Fatal(err)
		}
		items := HTMLMedia(doc, base)
		if len(items) != 1 || items[0].Kind != tc.kind || items[0].URL != "https://example.org"+tc.path {
			t.Fatalf("unexpected attachments %#v", items)
		}
	}
}
