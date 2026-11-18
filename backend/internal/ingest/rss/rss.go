package rss

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html/charset"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

type Collector struct {
	client *http.Client
}

func New(client *http.Client) *Collector {
	return &Collector{client: client}
}

type link struct {
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Text string `xml:",chardata"`
}

type text struct {
	Value string `xml:",chardata"`
	Raw   string `xml:",innerxml"`
}

func (t text) plain() string {
	if strings.TrimSpace(t.Value) != "" {
		return ingest.StripMarkup(t.Value)
	}
	return ingest.StripMarkup(t.Raw)
}

type entry struct {
	Enclosures []attachment `xml:"enclosure"`
	Media      []attachment `xml:"http://search.yahoo.com/mrss/ content"`
	MediaGroup struct {
		Media []attachment `xml:"http://search.yahoo.com/mrss/ content"`
	} `xml:"http://search.yahoo.com/mrss/ group"`
	Title       text   `xml:"title"`
	Links       []link `xml:"link"`
	GUID        string `xml:"guid"`
	ID          string `xml:"id"`
	Description text   `xml:"description"`
	Content     text   `xml:"content"`
	Encoded     text   `xml:"encoded"`
	Summary     text   `xml:"summary"`
	PubDate     string `xml:"pubDate"`
	Date        string `xml:"date"`
	Published   string `xml:"published"`
	Updated     string `xml:"updated"`
}

type attachment struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Medium string `xml:"medium,attr"`
}

func (item entry) attachments(base *url.URL) []model.Media {
	values := append(append([]attachment{}, item.Enclosures...), item.Media...)
	values = append(values, item.MediaGroup.Media...)
	for _, link := range item.Links {
		if link.Rel == "enclosure" {
			values = append(values, attachment{URL: link.Href, Type: link.Type})
		}
	}
	var result []model.Media
	seen := map[string]bool{}
	for _, value := range values {
		kind := ""
		if strings.HasPrefix(value.Type, "image/") || value.Medium == "image" {
			kind = "photo"
		}
		if strings.HasPrefix(value.Type, "video/") || value.Medium == "video" {
			kind = "video"
		}
		raw := ingest.ResolveLink(base, value.URL)
		if kind != "" && raw != "" && !seen[raw] && len(result) < 10 {
			result = append(result, model.Media{URL: raw, Kind: kind})
			seen[raw] = true
		}
	}
	return result
}

type document struct {
	XMLName xml.Name
	Channel struct {
		Items []entry `xml:"item"`
	} `xml:"channel"`
	Items   []entry `xml:"item"`
	Entries []entry `xml:"entry"`
}

func (c *Collector) Fetch(ctx context.Context, source model.Source) ([]model.Material, error) {
	page, err := ingest.Fetch(ctx, c.client, source.URL)
	if err != nil {
		return nil, err
	}
	return parse(page, source)
}

func parse(page ingest.Page, source model.Source) ([]model.Material, error) {
	var doc document
	decoder := xml.NewDecoder(bytes.NewReader(page.Body))
	decoder.CharsetReader = charset.NewReaderLabel
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse RSS or Atom feed: %w", err)
	}
	var entries []entry
	switch doc.XMLName.Local {
	case "rss":
		entries = doc.Channel.Items
	case "RDF":
		entries = doc.Items
	case "feed":
		entries = doc.Entries
	default:
		return nil, errors.New("source is not an RSS or Atom feed")
	}
	materials := make([]model.Material, 0, min(len(entries), ingest.MaxItems))
	for _, item := range entries {
		if len(materials) == ingest.MaxItems {
			break
		}
		id := item.ID
		if id == "" {
			id = item.GUID
		}
		rawLink := itemURL(item.Links, page.URL)
		if rawLink == "" && item.GUID != "" {
			if _, err := ingest.ParseURL(item.GUID); err == nil {
				rawLink = item.GUID
			}
		}
		title := item.Title.plain()
		if title == "" || rawLink == "" {
			continue
		}
		material := model.Material{
			Media:      item.attachments(page.URL),
			ExternalID: id,
			URL:        rawLink,
			Title:      title,
			Body:       firstText(item.Encoded, item.Content, item.Description, item.Summary),
		}
		for _, value := range []string{item.Published, item.PubDate, item.Date, item.Updated} {
			if stamp := ingest.ParseDate(value); !stamp.IsZero() {
				material.PublishedAt = stamp
				break
			}
		}
		materials = append(materials, ingest.Normalize(material, source))
	}
	return materials, nil
}

func itemURL(links []link, base *url.URL) string {
	for _, value := range links {
		if value.Rel != "" && value.Rel != "alternate" {
			continue
		}
		raw := value.Href
		if raw == "" {
			raw = value.Text
		}
		if resolved := ingest.ResolveLink(base, raw); resolved != "" {
			return resolved
		}
	}
	return ""
}

func firstText(values ...text) string {
	for _, value := range values {
		if plain := value.plain(); plain != "" {
			return plain
		}
	}
	return ""
}
