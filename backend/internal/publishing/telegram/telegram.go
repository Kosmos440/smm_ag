package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	endpoint string
	client   *http.Client
}

func New(token string) *Publisher {
	return &Publisher{
		endpoint: "https://api.telegram.org/bot" + token + "/sendMessage",
		client: &http.Client{
			Timeout: 15 * time.Second,
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
	payload := struct {
		ChatID string           `json:"chat_id"`
		Text   string           `json:"text"`
		Reply  *replyParameters `json:"reply_parameters,omitempty"`
	}{ChatID: post.Destination, Text: render(post, previousID != "")}
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
	title := post.Title
	if continuation {
		title = "Дополнение: " + title
	}
	suffix := ""
	if post.SourceURL != "" {
		suffix = "\n\nИсточник: " + limitUTF16(strings.TrimSpace(post.SourceURL), 1000)
	}
	text := strings.TrimSpace(title + "\n\n" + post.Body)
	return limitUTF16(text, 4096-utf16Len(suffix)) + suffix
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
