package website

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

func TestListAndArticleExtraction(t *testing.T) {
	base := &url.URL{Scheme: "https", Host: "example.org", Path: "/news"}
	for _, tc := range []struct {
		name, document, wantURL, wantTitle, wantBody string
	}{
		{"list", `<!doctype html><html><body><article><h2><a href="/news/1">Проверенная новость</a></h2><time datetime="2026-11-17T10:00:00Z"></time><p>Первый факт.</p></article><article><h2><a href="/undated">Без даты</a></h2></article></body></html>`, "https://example.org/news/1", "Проверенная новость", "Первый факт."},
		{"detail", `<!doctype html><html><head><meta property="og:title" content="Проверенная новость"><meta property="article:published_time" content="2026-11-17T10:00:00Z"><link rel="canonical" href="/news/2"></head><body><main><article><h1>Заголовок страницы</h1><p>Второй факт.</p></article></main></body></html>`, "https://example.org/news/2", "Проверенная новость", "Второй факт."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := parse(ingest.Page{URL: base, Body: []byte(tc.document)}, model.Source{})
			if err != nil || len(items) != 1 {
				t.Fatalf("parse page: %v, items %v", err, items)
			}
			item := items[0]
			if item.URL != tc.wantURL || item.Title != tc.wantTitle || item.Body != tc.wantBody || item.PublishedAt.IsZero() {
				t.Fatalf("unexpected article: %#v", item)
			}
		})
	}
}

func TestStructuredArticleFromGraph(t *testing.T) {
	page, _ := url.Parse("https://example.org/news/42?campaign=1")
	document := `<html><head>
		<link rel="canonical" href="/news/42">
		<script type="application/ld+json">{broken}</script>
		<script type="application/ld+json">[{"@graph":[
		  {"@type":"NewsArticle","url":"/news/99","headline":"Related headline","datePublished":"2026-11-16"},
		  {"@type":["Article","NewsArticle"],"mainEntityOfPage":{"@id":"/news/42"},
		   "headline":"Main headline","articleBody":["The publisher confirmed the change.","A second paragraph."],
		   "datePublished":"2026-11-17T13:30:00+03:00"}
		]}]</script></head>
		<body><article><h2><a href="/news/99">Related headline</a></h2>
		<time datetime="2026-11-16"></time></article></body></html>`

	items, err := parse(ingest.Page{URL: page, Body: []byte(document)}, model.Source{ID: 5})
	if err != nil || len(items) != 1 {
		t.Fatalf("parse page: %v, items %v", err, items)
	}
	item := items[0]
	wantDate := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	if item.URL != "https://example.org/news/42" || item.Title != "Main headline" ||
		item.Body != "The publisher confirmed the change. A second paragraph." || !item.PublishedAt.Equal(wantDate) || item.SourceID != 5 {
		t.Fatalf("unexpected article: %#v", item)
	}
}

func TestReadabilityExtractsMainText(t *testing.T) {
	page, _ := url.Parse("https://example.org/news/42")
	paragraph := strings.Repeat("The report explains how the new schedule affects local residents and describes the next steps. ", 8)
	document := `<html><head><title>Main story</title>
		<script type="application/ld+json">{
		  "@type":"NewsArticle","url":"/news/42#body","headline":"Main story",
		  "datePublished":"2026-11-17T10:30:00Z","articleBody":{"@type":"WebPageElement","@id":"#body"}
		}</script></head>
		<body><nav><p>Navigation marker</p></nav>
		<div class="article-body"><h1>Main story</h1><p>` + paragraph + `</p><p>Closing detail.</p></div>
		<footer><p>Footer marker</p></footer></body></html>`

	items, err := parse(ingest.Page{URL: page, Body: []byte(document)}, model.Source{})
	if err != nil || len(items) != 1 {
		t.Fatalf("parse page: %v, items %v", err, items)
	}
	body := items[0].Body
	if !strings.Contains(body, "Closing detail.") || !strings.Contains(body, strings.TrimSpace(paragraph)) ||
		strings.Contains(body, "Navigation marker") || strings.Contains(body, "Footer marker") {
		t.Fatalf("unexpected article body: %q", body)
	}
}
