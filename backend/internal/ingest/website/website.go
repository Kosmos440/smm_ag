package website

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

type Collector struct {
	client *http.Client
}

func New(client *http.Client) *Collector {
	return &Collector{client: client}
}

func (c *Collector) Fetch(ctx context.Context, source model.Source) ([]model.Material, error) {
	page, err := ingest.Fetch(ctx, c.client, source.URL)
	if err != nil {
		return nil, err
	}
	return parse(page, source)
}

func parse(page ingest.Page, source model.Source) ([]model.Material, error) {
	doc, err := ingest.ParseHTML(page)
	if err != nil {
		return nil, err
	}
	canonical := canonicalURL(doc, page.URL)
	structured := findStructuredArticle(doc, page.URL, canonical)
	articles := ingest.Descendants(doc, func(node *html.Node) bool {
		return node.Data == "article" || strings.Contains(ingest.Attribute(node, "itemtype"), "NewsArticle")
	})
	articlePage := structured != nil || strings.EqualFold(meta(doc, "og:type"), "article") ||
		meta(doc, "article:published_time") != "" ||
		len(articles) == 1 && ingest.First(articles[0], func(node *html.Node) bool { return node.Data == "h1" }) != nil
	if articlePage || len(articles) == 0 {
		if item, ok := readArticlePage(doc, page.URL, canonical, structured); ok {
			return []model.Material{ingest.Normalize(item, source)}, nil
		}
	}

	var materials []model.Material
	seen := make(map[string]struct{})
	for _, article := range articles {
		if len(materials) == ingest.MaxItems {
			break
		}
		material := readArticle(article, page.URL)
		if material.URL == "" || material.Title == "" || material.PublishedAt.IsZero() {
			continue
		}
		if _, exists := seen[material.URL]; exists {
			continue
		}
		seen[material.URL] = struct{}{}
		materials = append(materials, ingest.Normalize(material, source))
	}
	if len(materials) != 0 {
		return materials, nil
	}

	if !articlePage && len(articles) != 0 {
		if item, ok := readArticlePage(doc, page.URL, canonical, structured); ok {
			return []model.Material{ingest.Normalize(item, source)}, nil
		}
	}
	return nil, nil
}

func readArticle(root *html.Node, base *url.URL) model.Material {
	heading := ingest.First(root, func(node *html.Node) bool {
		return node.Data == "h1" || node.Data == "h2" || node.Data == "h3" ||
			ingest.Attribute(node, "itemprop") == "headline"
	})
	isLink := func(node *html.Node) bool {
		return node.Data == "a" && ingest.Attribute(node, "href") != ""
	}
	link := ingest.First(heading, isLink)
	if heading != nil && link == nil {
		for ancestor := heading; ancestor != root && ancestor != nil; ancestor = ancestor.Parent {
			if isLink(ancestor) {
				link = ancestor
				break
			}
		}
	}
	if link == nil {
		link = ingest.First(root, isLink)
	}
	title := ingest.Text(heading)
	if title == "" {
		title = ingest.Text(link)
	}

	bodyRoot := root
	if main := ingest.First(root, func(node *html.Node) bool { return node.Data == "main" }); main != nil {
		bodyRoot = main
	}
	paragraphs := ingest.Descendants(bodyRoot, func(node *html.Node) bool { return node.Data == "p" })
	texts := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		texts = append(texts, ingest.Text(paragraph))
	}
	material := model.Material{
		Media:       ingest.HTMLMedia(root, base),
		Title:       title,
		Body:        strings.Join(texts, " "),
		PublishedAt: publishedAt(root),
	}
	if link != nil {
		material.URL = ingest.ResolveLink(base, ingest.Attribute(link, "href"))
	}
	return material
}

func publishedAt(root *html.Node) time.Time {
	for _, name := range []string{"article:published_time", "datePublished", "date"} {
		if stamp := ingest.ParseDate(meta(root, name)); !stamp.IsZero() {
			return stamp
		}
	}
	dates := ingest.Descendants(root, func(node *html.Node) bool {
		return node.Data == "time" || ingest.Attribute(node, "itemprop") == "datePublished"
	})
	if root.Type == html.DocumentNode && len(dates) > 1 {
		return time.Time{}
	}
	for _, node := range dates {
		for _, raw := range []string{ingest.Attribute(node, "datetime"), ingest.Attribute(node, "content"), ingest.Text(node)} {
			if stamp := ingest.ParseDate(raw); !stamp.IsZero() {
				return stamp
			}
		}
	}
	return time.Time{}
}

func meta(root *html.Node, key string) string {
	node := ingest.First(root, func(node *html.Node) bool {
		return node.Data == "meta" && (ingest.Attribute(node, "property") == key ||
			ingest.Attribute(node, "name") == key || ingest.Attribute(node, "itemprop") == key)
	})
	if node == nil {
		return ""
	}
	return ingest.Attribute(node, "content")
}
