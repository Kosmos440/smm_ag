package rss

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

func TestRSSAndAtom(t *testing.T) {
	base := &url.URL{Scheme: "https", Host: "example.org", Path: "/feeds/news.xml"}
	source := model.Source{ID: 8, Name: "Редакция", Organization: "Редакция", Primary: true}
	for _, tc := range []struct {
		name, document, id, href string
	}{
		{"rss", `<?xml version="1.0"?><rss><channel><item><title>Важная новость</title><guid>42</guid><link>/post/42#comments</link><pubDate>Tue, 17 Nov 2026 12:30:00 +0300</pubDate><description><![CDATA[<p>Новый <b>заголовок</b></p>]]></description></item></channel></rss>`, "42", "https://example.org/post/42"},
		{"atom", `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>tag:news:1</id><title>Важная новость</title><link rel="self" href="/ignored"/><link rel="alternate" href="../post/1"/><published>2026-11-17T09:30:00Z</published><updated>2026-11-20T10:00:00Z</updated><content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml"><p>Новый <b>заголовок</b></p></div></content></entry></feed>`, "tag:news:1", "https://example.org/post/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := parse(ingest.Page{URL: base, Body: []byte(tc.document)}, source)
			if err != nil || len(items) != 1 {
				t.Fatalf("parse feed: %v, items %v", err, items)
			}
			item := items[0]
			if item.ExternalID != tc.id || item.URL != tc.href || item.Title != "Важная новость" || !strings.Contains(item.Body, "Новый заголовок") || item.SourceID != 8 || !item.Primary || !item.PublishedAt.Equal(time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)) {
				t.Fatalf("unexpected item: %#v", item)
			}
		})
	}
}

func TestMediaRSSAndEnclosure(t *testing.T) {
	base, _ := url.Parse("https://example.org/feed")
	raw := `<rss xmlns:media="http://search.yahoo.com/mrss/"><channel><item><title>News</title><link>https://example.org/story</link><description>Body</description><media:content url="https://example.org/clip.mp4" type="video/mp4"/><enclosure url="https://example.org/photo.jpg" type="image/jpeg"/></item></channel></rss>`
	items, err := parse(ingest.Page{URL: base, Body: []byte(raw)}, model.Source{})
	if err != nil || len(items) != 1 || len(items[0].Media) != 2 {
		t.Fatalf("attachments lost: %#v (%v)", items, err)
	}
	if items[0].Body != "Body" {
		t.Fatalf("media replaced content: %s", items[0].Body)
	}
}
