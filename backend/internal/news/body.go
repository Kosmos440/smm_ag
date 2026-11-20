package news

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A sentence can legitimately end with "просил читать далее". Lowercase
// navigation therefore needs a sentence or line boundary.
var readMoreSuffix = regexp.MustCompile(`(?i)читать[\s\p{Zs}]+далее[\s\p{Zs}.!…→›]*$`)

// PrepareBody removes a trailing navigation label and explains why a source
// with no independent text cannot become a post.
func PrepareBody(title, body string) (string, string) {
	body = strings.TrimSpace(body)
	if loc := readMoreSuffix.FindStringIndex(body); loc != nil {
		prefix := body[:loc[0]]
		before, _ := utf8.DecodeLastRuneInString(prefix)
		trimmed := strings.TrimRightFunc(prefix, unicode.IsSpace)
		last, _ := utf8.DecodeLastRuneInString(trimmed)
		if trimmed == "" || strings.ContainsRune(".!?…", last) ||
			strings.ContainsAny(prefix[len(trimmed):], "\r\n") ||
			(unicode.IsSpace(before) && strings.HasPrefix(body[loc[0]:], "Ч")) {
			body = trimmed
		}
	}
	switch {
	case strings.IndexFunc(body, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) < 0:
		return body, "нет самостоятельного текста"
	case comparableBody(body) == comparableBody(title):
		return body, "текст повторяет заголовок"
	default:
		return body, ""
	}
}

// Keep signs, units and punctuation inside numbers: +5%, -5% and 0.5%
// describe different facts even when the surrounding words match.
func comparableBody(value string) string {
	value = strings.Map(func(r rune) rune {
		r = unicode.ToLower(r)
		switch r {
		case 'ё':
			return 'е'
		case '«', '»', '“', '”', '"':
			return -1
		default:
			return r
		}
	}, value)
	return strings.TrimRight(strings.Join(strings.Fields(value), " "), ".!?… ")
}
