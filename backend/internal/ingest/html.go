package ingest

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

func ParseHTML(page Page) (*html.Node, error) {
	var reader io.Reader = bytes.NewReader(page.Body)
	if strings.Contains(strings.ToLower(page.ContentType), "charset") || !utf8.Valid(page.Body) {
		converted, err := charset.NewReader(reader, page.ContentType)
		if err != nil {
			return nil, fmt.Errorf("decode page HTML: %w", err)
		}
		reader = converted
	}
	node, err := html.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("parse page HTML: %w", err)
	}
	return node, nil
}

func Attribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func HasClass(node *html.Node, name string) bool {
	for _, class := range strings.Fields(Attribute(node, "class")) {
		if class == name {
			return true
		}
	}
	return false
}

// Descendants collects elements in document order, excluding root.
func Descendants(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var matches []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && match(child) {
				matches = append(matches, child)
			}
			visit(child)
		}
	}
	if root != nil {
		visit(root)
	}
	return matches
}

func First(root *html.Node, match func(*html.Node) bool) *html.Node {
	if root == nil {
		return nil
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && match(child) {
			return child
		}
		if found := First(child, match); found != nil {
			return found
		}
	}
	return nil
}

func Text(root *html.Node) string {
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
			return
		}
		switch node.Data {
		case "script", "style", "noscript", "template":
			return
		}
		block := false
		switch node.Data {
		case "article", "br", "div", "h1", "h2", "h3", "li", "p", "section", "time":
			block = true
		}
		if block {
			text.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if block {
			text.WriteByte('\n')
		}
	}
	if root != nil {
		visit(root)
	}
	return strings.TrimSpace(text.String())
}

func StripMarkup(markup string) string {
	if !strings.Contains(markup, "<") {
		return strings.TrimSpace(markup)
	}
	node, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return strings.TrimSpace(markup)
	}
	return Text(node)
}
