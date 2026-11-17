package telegram

import (
	"net/url"
	"testing"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

func TestPublicPreviewURLAndMessages(t *testing.T) {
	const expected = "https://t.me/s/example"
	for _, candidate := range []string{"https://t.me/Example", expected} {
		if got, err := PreviewURL(candidate); err != nil || got != expected {
			t.Fatalf("PreviewURL(%q) = %q, %v", candidate, got, err)
		}
	}
	for _, candidate := range []string{"http://t.me/example", "https://t.me.evil.example/example", "https://t.me/+invite", "https://t.me/example/123", "https://t.me/s/example?q=1"} {
		if _, err := PreviewURL(candidate); err == nil {
			t.Errorf("unexpected source accepted: %s", candidate)
		}
	}
	base, _ := url.Parse(expected)
	page := ingest.Page{URL: base, Body: []byte(`<section class="tgme_channel_history"><div class="tgme_widget_message" data-post="example/42"><div class="tgme_widget_message_text">Заголовок<br>Текст новости</div><time datetime="2026-11-17T10:00:00Z"></time></div><div class="tgme_widget_message" data-post="different/43"><div class="tgme_widget_message_text">Сторонняя новость</div><time datetime="2026-11-17T10:00:00Z"></time></div></section>`)}
	items, err := parse(page, model.Source{ID: 4}, "example")
	if err != nil || len(items) != 1 {
		t.Fatalf("parse preview: %v, items %v", err, items)
	}
	item := items[0]
	if item.SourceID != 4 || item.ExternalID != "example/42" || item.URL != "https://t.me/example/42" ||
		item.Title != "Заголовок" || item.Body != "Заголовок Текст новости" {
		t.Fatalf("unexpected post: %#v", item)
	}
}
