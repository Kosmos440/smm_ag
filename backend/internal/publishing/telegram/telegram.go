package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smmagent/internal/model"
	"smmagent/internal/publishing"
)

type Publisher struct {
	Media    MediaStore
	TempDir  string
	endpoint string
	client   *http.Client
}

type MediaStore interface {
	Open(context.Context, model.Media) (io.ReadCloser, error)
}

func New(token string) *Publisher {
	return &Publisher{
		endpoint: "https://api.telegram.org/bot" + token + "/sendMessage",
		client: &http.Client{
			Timeout: 90 * time.Second,
			// Never forward the token to a redirect destination.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			},
		},
	}
}

type replyParameters struct {
	MessageID int64 `json:"message_id"`
}

func (p *Publisher) Send(ctx context.Context, post model.Publication, previousID string) (string, error) {
	if post.ChannelName != "" {
		post = p.channelInfo(ctx, post)
	}
	if len(post.Media) > 0 {
		return p.sendMedia(ctx, post, previousID)
	}
	payload := struct {
		ParseMode string           `json:"parse_mode"`
		ChatID    string           `json:"chat_id"`
		Text      string           `json:"text"`
		Reply     *replyParameters `json:"reply_parameters,omitempty"`
	}{ChatID: post.Destination, Text: render(post, previousID != ""), ParseMode: "HTML"}
	if previousID != "" {
		id, err := strconv.ParseInt(previousID, 10, 64)
		if err != nil || id <= 0 {
			return "", &publishing.ResultError{Message: "invalid previous Telegram message ID"}
		}
		payload.Reply = &replyParameters{MessageID: id}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", &publishing.ResultError{Message: "encode Telegram message"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", &publishing.ResultError{Message: "invalid Telegram endpoint"}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return "", &publishing.ResultError{Message: "Telegram delivery outcome is unknown", Uncertain: true}
	}
	defer response.Body.Close()

	var reply struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
		ErrorCode int `json:"error_code"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply)
	if err == nil && response.StatusCode == http.StatusOK && reply.OK && reply.Result.MessageID > 0 {
		return strconv.FormatInt(reply.Result.MessageID, 10), nil
	}
	if err == nil && !reply.OK && reply.ErrorCode >= 400 && reply.ErrorCode < 500 {
		return "", &publishing.ResultError{Message: fmt.Sprintf("Telegram rejected the message (%d)", reply.ErrorCode)}
	}
	return "", &publishing.ResultError{Message: "Telegram delivery outcome is unknown", Uncertain: true}
}

func render(post model.Publication, continuation bool) string {
	return renderLimit(post, continuation, 4096)
}

func renderLimit(post model.Publication, continuation bool, limit int) string {
	title := post.Title
	if continuation {
		title = "Дополнение: " + title
	}
	suffix := ""
	if post.SourceURL != "" {
		suffix = "\n\nИсточник: " + escapeLimited(strings.TrimSpace(post.SourceURL), 250)
	}
	if post.ChannelName != "" {
		name := escapeLimited(post.ChannelName, 100)
		channel := strings.TrimPrefix(post.Destination, "@")
		link := post.ChannelURL
		if link == "" && strings.HasPrefix(post.Destination, "@") {
			link = "https://t.me/" + channel
		}
		if strings.HasPrefix(link, "https://t.me/") && utf16Len(link) <= 150 {
			suffix += "\n\n👉 <a href=\"" + html.EscapeString(link) + "\">" + name + ". Подписаться</a>"
		} else {
			suffix += "\n\n👉 " + name
		}
	}
	budget := limit - utf16Len(suffix) - 18
	heading := escapeLimited(title, min(250, max(0, budget)))
	body := escapeLimited(strings.TrimSpace(post.Body), max(0, budget-utf16Len(heading)))
	return "<b>" + heading + "</b>\n\n<i>" + body + "</i>" + suffix
}

// Prefer the actual Telegram channel title over the internal editorial name.
func (p *Publisher) channelInfo(ctx context.Context, post model.Publication) model.Publication {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(map[string]string{"chat_id": post.Destination})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(p.endpoint, "/sendMessage")+"/getChat", bytes.NewReader(raw))
	if err != nil {
		return post
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return post
	}
	defer response.Body.Close()
	var reply struct {
		OK     bool `json:"ok"`
		Result struct {
			Title    string `json:"title"`
			Username string `json:"username"`
			Invite   string `json:"invite_link"`
		} `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply) == nil && reply.OK {
		if reply.Result.Title != "" {
			post.ChannelName = reply.Result.Title
		}
		if reply.Result.Username != "" {
			post.ChannelURL = "https://t.me/" + reply.Result.Username
		} else {
			post.ChannelURL = reply.Result.Invite
		}
	}
	return post
}

func escapeLimited(value string, budget int) string {
	var result strings.Builder
	for _, r := range value {
		escaped := html.EscapeString(string(r))
		if utf16Len(escaped) > budget {
			break
		}
		result.WriteString(escaped)
		budget -= utf16Len(escaped)
	}
	return result.String()
}

func utf16Len(value string) int {
	n := 0
	for _, r := range value {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func limitUTF16(value string, limit int) string {
	if utf16Len(value) <= limit {
		return value
	}
	length := 0
	for index, r := range value {
		length++
		if r > 0xffff {
			length++
		}
		if length >= limit {
			return strings.TrimSpace(value[:index]) + "…"
		}
	}
	return value
}
