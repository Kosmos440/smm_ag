package website

import (
	"encoding/json"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

func readArticlePage(doc *html.Node, page *url.URL, canonical string, structured *structuredArticle) (model.Material, bool) {
	material := model.Material{URL: canonical}
	if material.URL == "" {
		material.URL = page.String()
	}
	if structured != nil {
		material.Title = strings.TrimSpace(structured.Headline)
		material.Body = structured.bodyText()
		material.PublishedAt = ingest.ParseDate(structured.DatePublished)
	}
	if material.PublishedAt.IsZero() {
		material.PublishedAt = publishedAt(doc)
	}
	if material.PublishedAt.IsZero() {
		return model.Material{}, false
	}
	if material.Title == "" {
		material.Title = strings.TrimSpace(meta(doc, "og:title"))
	}
	if material.Body == "" || material.Title == "" {
		if article, err := readability.FromDocument(doc, page); err == nil {
			if material.Body == "" {
				heading := ingest.First(article.Node, func(node *html.Node) bool {
					return node.Data == "h1" || node.Data == "h2"
				})
				pageHeading := ingest.First(doc, func(node *html.Node) bool { return node.Data == "h1" })
				if heading != nil {
					text := strings.Join(strings.Fields(ingest.Text(heading)), " ")
					if text != "" && (text == strings.Join(strings.Fields(material.Title), " ") ||
						text == strings.Join(strings.Fields(ingest.Text(pageHeading)), " ") ||
						text == strings.Join(strings.Fields(article.Title()), " ")) {
						heading.Parent.RemoveChild(heading)
					}
				}
				var body strings.Builder
				if err := article.RenderText(&body); err == nil {
					material.Body = strings.TrimSpace(body.String())
				}
			}
			if material.Title == "" {
				material.Title = strings.TrimSpace(article.Title())
			}
		}
	}
	if material.Title == "" || material.Body == "" {
		// Preserve the basic extraction for short articles that Readability skips.
		fallback := readArticle(doc, page)
		if material.Title == "" {
			material.Title = fallback.Title
		}
		if material.Body == "" {
			material.Body = fallback.Body
		}
	}
	if material.Body == "" {
		material.Body = strings.TrimSpace(meta(doc, "description"))
	}
	return material, material.Title != ""
}

func canonicalURL(doc *html.Node, page *url.URL) string {
	canonical := ingest.First(doc, func(node *html.Node) bool {
		return node.Data == "link" && strings.EqualFold(ingest.Attribute(node, "rel"), "canonical")
	})
	if canonical == nil {
		return ""
	}
	return ingest.ResolveLink(page, ingest.Attribute(canonical, "href"))
}

type structuredArticle struct {
	Type             any             `json:"@type"`
	Headline         string          `json:"headline"`
	ArticleBody      json.RawMessage `json:"articleBody"`
	DatePublished    string          `json:"datePublished"`
	URL              string          `json:"url"`
	ID               string          `json:"@id"`
	MainEntityOfPage json.RawMessage `json:"mainEntityOfPage"`
	Graph            json.RawMessage `json:"@graph"`
	MainEntity       json.RawMessage `json:"mainEntity"`
}

func (article structuredArticle) bodyText() string {
	var body string
	if json.Unmarshal(article.ArticleBody, &body) == nil {
		return ingest.StripMarkup(body)
	}
	var paragraphs []string
	if json.Unmarshal(article.ArticleBody, &paragraphs) == nil {
		return ingest.StripMarkup(strings.Join(paragraphs, "\n"))
	}
	return ""
}

func findStructuredArticle(doc *html.Node, page *url.URL, canonical string) *structuredArticle {
	scripts := ingest.Descendants(doc, func(node *html.Node) bool {
		return node.Data == "script" && strings.EqualFold(strings.TrimSpace(ingest.Attribute(node, "type")), "application/ld+json")
	})
	var matches []structuredArticle
	for _, script := range scripts {
		if script.FirstChild != nil {
			readStructuredArticles(json.RawMessage(script.FirstChild.Data), 0, &matches)
		}
	}

	var unlinked *structuredArticle
	for i := range matches {
		article := &matches[i]
		identity := article.pageReference()
		if identity == "" {
			unlinked = article
			continue
		}
		if resolved := ingest.ResolveLink(page, identity); resolved != "" &&
			(resolved == canonical || resolved == page.String()) {
			return article
		}
		if strings.HasPrefix(identity, "#") && len(matches) == 1 {
			unlinked = article
		}
	}
	// A single article without an explicit URL can describe the page itself.
	if len(matches) == 1 {
		return unlinked
	}
	return nil
}

func readStructuredArticles(raw json.RawMessage, depth int, matches *[]structuredArticle) {
	if len(raw) == 0 || depth >= 12 {
		return
	}
	var article structuredArticle
	if err := json.Unmarshal(raw, &article); err == nil {
		if articleType(article.Type) {
			*matches = append(*matches, article)
		}
		readStructuredArticles(article.Graph, depth+1, matches)
		readStructuredArticles(article.MainEntity, depth+1, matches)
		return
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err == nil {
		for _, entry := range entries {
			readStructuredArticles(entry, depth+1, matches)
		}
	}
}

func articleType(value any) bool {
	switch kind := value.(type) {
	case string:
		kind = strings.TrimPrefix(strings.TrimPrefix(kind, "https://schema.org/"), "http://schema.org/")
		return kind == "Article" || kind == "BlogPosting" || strings.HasSuffix(kind, "NewsArticle")
	case []any:
		for _, entry := range kind {
			if name, ok := entry.(string); ok && articleType(name) {
				return true
			}
		}
	}
	return false
}

func (article structuredArticle) pageReference() string {
	var reference string
	if err := json.Unmarshal(article.MainEntityOfPage, &reference); err != nil {
		var page struct {
			ID  string `json:"@id"`
			URL string `json:"url"`
		}
		if json.Unmarshal(article.MainEntityOfPage, &page) == nil {
			reference = page.ID
			if reference == "" {
				reference = page.URL
			}
		}
	}
	for _, candidate := range []string{reference, article.URL, article.ID} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}
