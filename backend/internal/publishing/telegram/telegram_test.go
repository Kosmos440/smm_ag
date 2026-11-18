package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smmagent/internal/model"
	"smmagent/internal/publishing"
)

func TestContinuationRepliesToPreviousMessage(t *testing.T) {
	publisher := testPublisher(func(w http.ResponseWriter, request *http.Request) {
		var payload struct {
			ChatID string          `json:"chat_id"`
			Text   string          `json:"text"`
			Reply  replyParameters `json:"reply_parameters"`
		}
		err := json.NewDecoder(request.Body).Decode(&payload)
		if err != nil || payload.ChatID != "@channel" || payload.Reply.MessageID != 42 || !strings.HasPrefix(payload.Text, "<b>Дополнение: ") {
			t.Errorf("invalid request: %+v (%v)", payload, err)
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":43}}`)
	})
	id, err := publisher.Send(t.Context(), model.Publication{Destination: "@channel", Title: "Новое сообщение"}, "42")
	if err != nil || id != "43" {
		t.Fatalf("message ID = %q; error = %v", id, err)
	}
}

func TestUndeterminedResponseIsUncertain(t *testing.T) {
	publisher := testPublisher(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream error", http.StatusBadGateway)
	})
	_, err := publisher.Send(t.Context(), model.Publication{Destination: "@channel", Title: "Пост"}, "")
	var outcome *publishing.ResultError
	if !errors.As(err, &outcome) || !outcome.Uncertain {
		t.Fatalf("expected an uncertain delivery, got %v", err)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testPublisher(handler http.HandlerFunc) Publisher {
	client := &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
		defer request.Body.Close()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})}
	return Publisher{endpoint: "https://example.org/sendMessage", client: client}
}

func TestRenderPreservesSourceAndUTF16Limit(t *testing.T) {
	const source = "https://example.org/story"
	text := render(model.Publication{Title: "Новость", Body: strings.Repeat("🙂", 3000), SourceURL: source}, false)
	if utf16Len(text) > 4096 || !strings.HasSuffix(text, source) {
		t.Fatalf("unexpected length %d or missing source", utf16Len(text))
	}
}
