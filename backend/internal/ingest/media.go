package ingest

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"smmagent/internal/model"
)

var backgroundURL = regexp.MustCompile(`url\(['"]?([^'")]+)`)

// HTMLMedia reads explicit article attachments and Open Graph originals.
// Video posters and page-wide images are excluded from video attachments.
func HTMLMedia(root *html.Node, base *url.URL) []model.Media {
	var media []model.Media
	add := func(raw, kind string) {
		resolved := ResolveLink(base, raw)
		if resolved == "" || len(media) >= 10 {
			return
		}
		for _, item := range media {
			if item.URL == resolved {
				return
			}
		}
		media = append(media, model.Media{URL: resolved, Kind: kind})
	}
	for _, node := range Descendants(root, func(n *html.Node) bool { return n.Data == "video" }) {
		add(Attribute(node, "src"), "video")
		for _, child := range Descendants(node, func(n *html.Node) bool { return n.Data == "source" }) {
			add(Attribute(child, "src"), "video")
		}
	}
	for _, node := range Descendants(root, func(n *html.Node) bool { return n.Data == "meta" }) {
		key := Attribute(node, "property")
		if key == "og:video" || key == "og:video:url" || key == "og:video:secure_url" {
			raw := Attribute(node, "content")
			if u, err := url.Parse(raw); err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".mp4") {
				add(raw, "video")
			}
		}
	}
	if len(media) > 0 {
		return media
	}
	for _, node := range Descendants(root, func(n *html.Node) bool {
		return n.Data == "meta" || n.Data == "img" || HasClass(n, "tgme_widget_message_photo_wrap")
	}) {
		switch {
		case node.Data == "meta" && Attribute(node, "property") == "og:image":
			add(Attribute(node, "content"), "photo")
		case HasClass(node, "tgme_widget_message_photo_wrap"):
			if match := backgroundURL.FindStringSubmatch(Attribute(node, "style")); len(match) == 2 {
				add(match[1], "photo")
			}
		case node.Data == "img":
			// Only images within an article body, never avatars or site navigation.
			for parent := node.Parent; parent != nil; parent = parent.Parent {
				if parent.Data == "article" {
					add(Attribute(node, "src"), "photo")
					break
				}
			}
		}
	}
	return media
}
