package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

var channelPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

type Collector struct {
	client *http.Client
}

func New(client *http.Client) *Collector {
	return &Collector{client: client}
}

// PreviewURL identifies a public channel whose posts Telegram exposes on the web.
func PreviewURL(raw string) (string, error) {
	parsed, err := ingest.ParseURL(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "t.me") ||
		parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") {
		return "", errors.New("Telegram source must be an HTTPS public t.me channel")
	}
	path := strings.Trim(strings.TrimSpace(parsed.EscapedPath()), "/")
	channel := strings.TrimPrefix(path, "s/")
	if !channelPattern.MatchString(channel) {
		return "", errors.New("Telegram source must identify one public channel")
	}
	return "https://t.me/s/" + strings.ToLower(channel), nil
}

func (c *Collector) Fetch(ctx context.Context, source model.Source) ([]model.Material, error) {
	preview, err := PreviewURL(source.URL)
	if err != nil {
		return nil, err
	}
	page, err := ingest.Fetch(ctx, c.client, preview)
	if err != nil {
		return nil, err
	}
	if final, err := PreviewURL(page.URL.String()); err != nil || final != preview {
		return nil, errors.New("Telegram request left the public channel preview")
	}
	return parse(page, source, strings.TrimPrefix(preview, "https://t.me/s/"))
}

func parse(page ingest.Page, source model.Source, channel string) ([]model.Material, error) {
	doc, err := ingest.ParseHTML(page)
	if err != nil {
		return nil, err
	}
	posts := ingest.Descendants(doc, func(node *html.Node) bool {
		return ingest.HasClass(node, "tgme_widget_message") && ingest.Attribute(node, "data-post") != ""
	})
	if len(posts) == 0 && ingest.First(doc, func(node *html.Node) bool {
		return ingest.HasClass(node, "tgme_channel_history")
	}) == nil {
		return nil, errors.New("Telegram did not return a channel history preview")
	}
	materials := make([]model.Material, 0, min(len(posts), ingest.MaxItems))
	for _, post := range posts {
		if len(materials) == ingest.MaxItems {
			break
		}
		postChannel, rawID, ok := strings.Cut(ingest.Attribute(post, "data-post"), "/")
		id, err := strconv.ParseInt(rawID, 10, 64)
		if !ok || err != nil || id <= 0 || !strings.EqualFold(postChannel, channel) {
			continue
		}
		text := ingest.First(post, func(node *html.Node) bool {
			return ingest.HasClass(node, "tgme_widget_message_text")
		})
		body := ingest.Text(text)
		title := firstLine(body)
		if title == "" {
			continue
		}
		clock := ingest.First(post, func(node *html.Node) bool { return node.Data == "time" })
		if clock == nil {
			continue
		}
		stamp := ingest.ParseDate(ingest.Attribute(clock, "datetime"))
		if stamp.IsZero() {
			continue
		}
		material := model.Material{
			ExternalID:  fmt.Sprintf("%s/%d", channel, id),
			URL:         fmt.Sprintf("https://t.me/%s/%d", channel, id),
			Title:       title,
			Body:        body,
			PublishedAt: stamp,
		}
		materials = append(materials, ingest.Normalize(material, source))
	}
	return materials, nil
}

func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.IndexFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
