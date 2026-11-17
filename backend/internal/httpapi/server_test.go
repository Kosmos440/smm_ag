package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smmagent/internal/channels"
	"smmagent/internal/model"
)

type testStore struct {
	channels.Store
	source   model.Source
	channel  model.Channel
	reviewed int64
	approved bool
	posts    []model.Publication
}

func TestFilterSuggestions(t *testing.T) {
	service := channels.New(&testStore{}).WithFilterGenerator(func(_ context.Context, input channels.FilterRequest) (channels.FilterSuggestion, error) {
		if input.Topic != "Новости энергетики" || input.Exclusions != "Реклама трейдинга" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return channels.FilterSuggestion{
			Keywords:     []string{"нефть", "НЕФТЬ", "энергорынок"},
			BlockedTerms: []string{"сигналы платного клуба"},
		}, nil
	})
	handler := New(service, func(context.Context) error { return nil })
	response := request(handler, "POST", "/api/v1/filter-suggestions", "application/json",
		`{"topic":"Новости энергетики","audience":"инвесторы","exclusions":"Реклама трейдинга"}`)
	var result channels.FilterSuggestion
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil ||
		len(result.Keywords) != 2 || len(result.BlockedTerms) != 1 {
		t.Fatalf("unexpected suggestions: %d %s", response.Code, response.Body.String())
	}
}

func (s *testStore) CreateSource(_ context.Context, source model.Source) (model.Source, error) {
	source.ID = 42
	s.source = source
	return source, nil
}

func (s *testStore) GetSource(_ context.Context, id int64) (model.Source, error) {
	if id != s.source.ID {
		return model.Source{}, model.ErrNotFound
	}
	return s.source, nil
}

func (s *testStore) CreateChannel(_ context.Context, channel model.Channel) (model.Channel, error) {
	channel.ID = 19
	s.channel = channel
	return channel, nil
}

func (s *testStore) GetChannel(_ context.Context, id int64) (model.Channel, error) {
	if id != s.channel.ID {
		return model.Channel{}, model.ErrNotFound
	}
	return s.channel, nil
}

func (s *testStore) ListChannels(context.Context, int, int) ([]model.Channel, error) {
	return nil, nil
}

func (s *testStore) DecideReview(_ context.Context, id int64, approved bool) (model.Publication, error) {
	s.reviewed = id
	s.approved = approved
	return model.Publication{ID: id, State: model.PublicationPending}, nil
}

func (s *testStore) ListSiteNews(context.Context, int64, int, int) ([]model.Publication, error) {
	return s.posts, nil
}

func request(handler http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestCreateAndReadChannel(t *testing.T) {
	store := &testStore{}
	handler := New(channels.New(store), func(context.Context) error { return nil })
	body := `{"name":" Regulations ","website":true,"enabled":true,"settings":{"topic":"Marking","audience":"Sellers","keywords":[" Tax ","tax"],"quiet_start_hour":22,"quiet_end_hour":7}}`
	w := request(handler, http.MethodPost, prefix+"/channels", "application/json", body)
	if w.Code != http.StatusCreated || w.Header().Get("Location") != prefix+"/channels/19" {
		t.Fatalf("create status %d, Location %q, body %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	settings := store.channel.Settings
	if store.channel.Name != "Regulations" || settings.MaxAgeMinutes != 1440 || settings.ReserveAfterMinutes != 30 ||
		settings.TimeZone != "UTC" || len(settings.Keywords) != 1 || settings.Keywords[0] != "Tax" {
		t.Fatalf("unexpected normalized channel: %+v", store.channel)
	}
	w = request(handler, http.MethodGet, prefix+"/channels/19", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Regulations"`) {
		t.Fatalf("get status %d, body %s", w.Code, w.Body)
	}
}

func TestSourceAndReviewOperations(t *testing.T) {
	store := &testStore{}
	handler := New(channels.New(store), func(context.Context) error { return nil })
	w := request(handler, http.MethodPost, prefix+"/sources", "application/json; charset=utf-8",
		`{"name":"Regulator","organization":"Agency","kind":"telegram","url":"https://t.me/Example_Channel"}`)
	if w.Code != http.StatusCreated || store.source.Status != model.SourceCandidate ||
		store.source.URL != "https://t.me/s/example_channel" {
		t.Fatalf("create status %d, source %+v, body %s", w.Code, store.source, w.Body)
	}
	w = request(handler, http.MethodPut, prefix+"/review/23", "application/json", `{"decision":"approve"}`)
	if w.Code != http.StatusOK || store.reviewed != 23 || !store.approved {
		t.Fatalf("review status %d, body %s", w.Code, w.Body)
	}
}

func TestRequestErrors(t *testing.T) {
	store := &testStore{}
	handler := New(channels.New(store), func(context.Context) error { return errors.New("private DB detail") })
	tests := []struct {
		name, method, path, contentType, body, code string
		status                                      int
	}{
		{"readiness", "GET", "/api/ready", "", "", "not_ready", 503},
		{"not found", "GET", prefix + "/channels/7", "", "", "not_found", 404},
		{"bad pagination", "GET", prefix + "/channels?limit=101", "", "", "invalid_page", 400},
		{"bad id", "GET", prefix + "/channels/0", "", "", "invalid_id", 400},
		{"unsupported type", "POST", prefix + "/channels", "text/plain", `{}`, "unsupported_media_type", 415},
		{"unknown field", "POST", prefix + "/channels", "application/json", `{"telegram_token":"secret"}`, "invalid_json", 400},
		{"extra value", "POST", prefix + "/channels", "application/json", `{}{}`, "invalid_json", 400},
		{"large body", "POST", prefix + "/channels", "application/json", `{"name":"` + strings.Repeat("x", maxRequestSize) + `"}`, "body_too_large", 413},
		{"quiet hours", "POST", prefix + "/channels", "application/json", `{"name":"News","website":true,"settings":{"topic":"t","audience":"a","quiet_start_hour":22}}`, "invalid_input", 400},
		{"private source", "POST", prefix + "/sources", "application/json", `{"name":"Local","organization":"Private","kind":"rss","url":"http://127.0.0.1/feed"}`, "invalid_input", 400},
		{"decision", "PUT", prefix + "/review/7", "application/json", `{"decision":"other"}`, "invalid_input", 400},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := request(handler, test.method, test.path, test.contentType, test.body)
			var response errorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if w.Code != test.status || response.Error.Code != test.code {
				t.Fatalf("got HTTP %d (%s), want HTTP %d (%s)", w.Code, response.Error.Code, test.status, test.code)
			}
			if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private DB detail") {
				t.Fatalf("private value returned: %s", w.Body)
			}
		})
	}
}

func TestSiteFeedProjectionAndEmptyList(t *testing.T) {
	store := &testStore{
		posts: []model.Publication{{
			ID: 3, ChannelID: 2, EventID: 1, Version: 1, State: model.PublicationSent,
			Target: model.TargetWebsite, Title: "Updated rule", Body: "Published text",
			Reason: "internal review", SentAt: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
		}},
	}
	handler := New(channels.New(store), func(context.Context) error { return nil })
	w := request(handler, "GET", prefix+"/site/news?channel_id=2&limit=2", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"published_at":"2026-11-17T10:00:00Z"`) ||
		strings.Contains(w.Body.String(), "internal review") {
		t.Fatalf("feed status %d, body %s", w.Code, w.Body)
	}
	w = request(handler, "GET", prefix+"/channels", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("empty list status %d, body %s", w.Code, w.Body)
	}
}

func TestProbeSource(t *testing.T) {
	store := &testStore{source: model.Source{ID: 2, Status: model.SourceCandidate}}
	calls := 0
	service := channels.New(store).WithSourceProbe(func(_ context.Context, source model.Source) ([]model.Material, error) {
		calls++
		if source.ID != 2 || source.Status != model.SourceCandidate {
			t.Fatalf("unexpected source: %+v", source)
		}
		if calls > 1 {
			return nil, errors.New("private upstream error")
		}
		materials := make([]model.Material, 6)
		for i := range materials {
			materials[i] = model.Material{Title: "Heading", URL: "https://example.org/article", Body: "unpublished full text"}
		}
		return materials, nil
	})
	handler := New(service, func(context.Context) error { return nil })
	path := prefix + "/sources/2/probe"
	w := request(handler, "POST", path, "", "")
	var response struct {
		Items []channels.SourcePreview `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if w.Code != http.StatusOK || len(response.Items) != 5 || response.Items[0].Title != "Heading" ||
		strings.Contains(w.Body.String(), "unpublished full text") || store.source.Status != model.SourceCandidate ||
		!store.source.LastSuccess.IsZero() {
		t.Fatalf("preview status %d, source %+v, body %s", w.Code, store.source, w.Body)
	}
	w = request(handler, "POST", path, "", "")
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "private upstream error") {
		t.Fatalf("probe error status %d, body %s", w.Code, w.Body)
	}
	store.source.Status = model.SourceDisabled
	w = request(handler, "POST", path, "", "")
	if w.Code != http.StatusConflict || calls != 2 {
		t.Fatalf("disabled probe status %d, callback calls %d", w.Code, calls)
	}
}
