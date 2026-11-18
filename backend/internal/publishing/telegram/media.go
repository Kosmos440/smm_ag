package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"smmagent/internal/model"
	"smmagent/internal/publishing"
)

func (p *Publisher) sendMedia(ctx context.Context, post model.Publication, previous string) (string, error) {
	if p.Media == nil {
		return "", &publishing.ResultError{Message: "S3 media storage is not configured"}
	}
	if len(post.Media) > 10 {
		return "", &publishing.ResultError{Message: "Telegram supports at most 10 attachments"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	file, err := os.CreateTemp(p.TempDir, "telegram-upload-*")
	if err != nil {
		return "", &publishing.ResultError{Message: "cannot create Telegram staging file"}
	}
	defer os.Remove(file.Name())
	defer file.Close()
	writer := multipart.NewWriter(file)
	fields := map[string]string{"chat_id": post.Destination}
	if previous != "" {
		id, err := strconv.ParseInt(previous, 10, 64)
		if err != nil || id <= 0 {
			return "", &publishing.ResultError{Message: "invalid previous Telegram message ID"}
		}
		reply, _ := json.Marshal(replyParameters{MessageID: id})
		fields["reply_parameters"] = string(reply)
	}
	method := "sendMediaGroup"
	var album []map[string]string
	for index, item := range post.Media {
		body, err := p.Media.Open(ctx, item)
		if err != nil {
			return "", &publishing.ResultError{Message: err.Error()}
		}
		field := fmt.Sprintf("file%d", index)
		filename := field + ".jpg"
		if item.Kind == "video" {
			filename = field + ".mp4"
		}
		entry := map[string]string{"type": item.Kind, "media": "attach://" + field}
		if index == 0 {
			entry["caption"] = renderLimit(post, previous != "", 1024)
			entry["parse_mode"] = "HTML"
		}
		if len(post.Media) == 1 {
			field = item.Kind
			method = "sendPhoto"
			if item.Kind == "video" {
				method = "sendVideo"
				fields["supports_streaming"] = "true"
			}
			fields["caption"] = entry["caption"]
			fields["parse_mode"] = "HTML"
		}
		part, partErr := writer.CreateFormFile(field, filename)
		if partErr == nil {
			_, partErr = io.Copy(part, body)
		}
		closeErr := body.Close()
		if errors.Join(partErr, closeErr) != nil {
			return "", &publishing.ResultError{Message: "cannot stage media from S3"}
		}
		album = append(album, entry)
	}
	if len(album) > 1 {
		raw, _ := json.Marshal(album)
		fields["media"] = string(raw)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return "", &publishing.ResultError{Message: "cannot write media request"}
		}
	}
	if err := writer.Close(); err != nil {
		return "", &publishing.ResultError{Message: "cannot finalize media request"}
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", &publishing.ResultError{Message: "cannot determine media request size"}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", &publishing.ResultError{Message: "cannot read media request"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(p.endpoint, "/sendMessage")+"/"+method, file)
	if err != nil {
		return "", &publishing.ResultError{Message: "invalid Telegram endpoint"}
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.ContentLength = size
	response, err := p.client.Do(request)
	if err != nil {
		return "", &publishing.ResultError{Message: "Telegram media delivery outcome is unknown", Uncertain: true}
	}
	defer response.Body.Close()
	var reply struct {
		OK        bool            `json:"ok"`
		ErrorCode int             `json:"error_code"`
		Result    json.RawMessage `json:"result"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&reply)
	if err == nil && response.StatusCode == http.StatusOK && reply.OK {
		var messages []struct {
			ID int64 `json:"message_id"`
		}
		result := reply.Result
		if method != "sendMediaGroup" {
			result = append(append([]byte("["), result...), ']')
		}
		if json.Unmarshal(result, &messages) == nil && len(messages) > 0 && messages[0].ID > 0 {
			return strconv.FormatInt(messages[0].ID, 10), nil
		}
	}
	if err == nil && !reply.OK && reply.ErrorCode >= 400 && reply.ErrorCode < 500 {
		return "", &publishing.ResultError{Message: fmt.Sprintf("Telegram rejected media (%d)", reply.ErrorCode)}
	}
	return "", &publishing.ResultError{Message: "Telegram media delivery outcome is unknown", Uncertain: true}
}
