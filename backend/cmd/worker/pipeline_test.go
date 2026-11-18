package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"smmagent/internal/channels"
	"smmagent/internal/httpapi"
	"smmagent/internal/ingest"
	"smmagent/internal/model"
	"smmagent/internal/postgres"
	"smmagent/internal/publishing"
	"smmagent/internal/publishing/website"
	"smmagent/internal/topic"
)

func TestWorkerPipeline(t *testing.T) {
	store := pipelineStore(t)
	handler := httpapi.New(channels.New(store), store.Ready)
	requestJSON(t, handler, http.MethodPost, "/api/v1/sources", `{
		"name":"Official notices","organization":"Regulator","url":"https://example.org/feed",
		"kind":"rss","status":"verified","primary":true
	}`, http.StatusCreated)
	requestJSON(t, handler, http.MethodPost, "/api/v1/sources", `{
		"name":"Unverified","organization":"Other","url":"https://example.org/candidate",
		"kind":"rss","status":"candidate"
	}`, http.StatusCreated)
	requestJSON(t, handler, http.MethodPost, "/api/v1/channels", `{
		"name":"News","telegram_chat_id":"@smm_test","website":true,"enabled":true,
		"settings":{"topic":"маркировка","keywords":["маркировки"],"audience":"малый бизнес","max_age_minutes":60}
	}`, http.StatusCreated)

	now := time.Now().UTC()
	fetcher := &fixtureFeed{material: model.Material{
		ExternalID:  "notice-73",
		URL:         "https://example.org/notice-73",
		Title:       "Новые правила маркировки вступают в силу " + now.AddDate(0, 0, 2).Format(time.DateOnly),
		Body:        "Обязательная маркировка вводится для продавцов. Документ № 73.",
		PublishedAt: now.Add(-time.Minute),
	}}
	sender := &recordedTelegram{}
	worker := scheduler{
		store: store, collectInterval: time.Minute,
		fetchers: map[model.SourceKind]ingest.Fetcher{model.SourceRSS: fetcher},
		publishers: map[model.Target]publishing.Publisher{
			model.TargetTelegram: sender, model.TargetWebsite: website.Publisher{},
		},
	}
	cycle := func() {
		t.Helper()
		worker.nextCollection = time.Time{}
		if err := worker.cycle(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	cycle()
	cycle()
	if len(sender.replies) != 1 || sender.replies[0] != "" {
		t.Fatalf("expected one main post across two collections: %v", sender.replies)
	}
	fetcher.material.Title = "Новые правила маркировки вступают в силу " + now.AddDate(0, 0, 3).Format(time.DateOnly)
	cycle()
	if len(sender.replies) != 2 || sender.replies[1] != "1" {
		t.Fatalf("expected one continuation replying to the main post: %v", sender.replies)
	}
	response := requestJSON(t, handler, http.MethodGet, "/api/v1/site/news", "", http.StatusOK)
	var feed struct {
		Items []channels.SitePost `json:"items"`
	}
	if err := json.Unmarshal(response, &feed); err != nil || len(feed.Items) != 2 ||
		feed.Items[0].EventID != feed.Items[1].EventID || feed.Items[0].Version != 2 {
		t.Fatalf("unexpected website feed: %s (%v)", response, err)
	}
}

func TestTopicPipelineWaitsForSharedArticle(t *testing.T) {
	store := pipelineStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	source, err := store.CreateSource(ctx, model.Source{URL: "https://example.org/feed", Name: "Feed",
		Organization: "Agency", Kind: model.SourceRSS, Status: model.SourceVerified, Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	var channelsToEvaluate []model.Channel
	for _, name := range []string{"Новости", "Парламент"} {
		channel, err := store.CreateChannel(ctx, model.Channel{Name: name, Website: true, Enabled: true,
			Settings: model.Settings{Topic: "политика", Audience: "читатели", Keywords: []string{"госдума"}, MaxAgeMinutes: 60}})
		if err != nil {
			t.Fatal(err)
		}
		channelsToEvaluate = append(channelsToEvaluate, channel)
	}
	material := model.Material{Title: "Депутаты обсудили проект",
		URL: "https://example.org/story", FetchedAt: now, PublishedAt: now.Add(-time.Minute)}
	if err := store.SaveMaterials(ctx, source.ID, []model.Material{material}); err != nil {
		t.Fatal(err)
	}
	worker := scheduler{store: store, articleFetch: func(context.Context, model.Material) (model.Material, error) {
		t.Fatal("evaluation must queue, rather than fetch, the article")
		return model.Material{}, nil
	}}
	for _, channel := range channelsToEvaluate {
		if err := worker.evaluate(ctx, channel, now); err != nil {
			t.Fatal(err)
		}
		if records, err := store.ListTopics(ctx, channel.ID, 10, 0); err != nil || len(records) != 0 {
			t.Fatalf("pending article evaluated: %v (%v)", records, err)
		}
	}
	job, err := store.ClaimArticle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lead := "В Госдуме обсудили проект об условиях участия граждан в работе местного самоуправления."
	if err := store.FinishArticle(ctx, job, lead, lead, nil); err != nil {
		t.Fatal(err)
	}
	for _, channel := range channelsToEvaluate {
		if err := worker.evaluate(ctx, channel, now); err != nil {
			t.Fatal(err)
		}
		records, err := store.ListTopics(ctx, channel.ID, 10, 0)
		if err != nil || len(records) != 1 || records[0].Decision == nil ||
			records[0].Decision.Method != "word_forms" || records[0].Decision.Verdict != model.TopicRelevant {
			t.Fatalf("missing morphology decision: %+v (%v)", records, err)
		}
	}
	channel := channelsToEvaluate[0]
	handler := httpapi.New(channels.New(store).WithTopicPreview(func(_ context.Context, ch model.Channel, id int64) (model.TopicDecision, error) {
		if ch.ID != channel.ID || id != job.Material.ID {
			t.Fatal("preview received a different item")
		}
		return model.TopicDecision{Verdict: model.TopicIrrelevant, Method: "keywords", Reason: "preview",
			ProfileVersion: topic.ProfileVersion(ch.Settings)}, nil
	}), store.Ready)
	path := fmt.Sprintf("/api/v1/channels/%d/topic-evaluations", channel.ID)
	item := fmt.Sprintf("%s/%d", path, job.Material.ID)
	requestJSON(t, handler, http.MethodPost, item+"/preview", "", http.StatusOK)
	requestJSON(t, handler, http.MethodPut, item+"/review", `{"verdict":"insufficient"}`, http.StatusNoContent)
	var result struct {
		Items []model.TopicRecord `json:"items"`
	}
	if err := json.Unmarshal(requestJSON(t, handler, http.MethodGet, path, "", http.StatusOK), &result); err != nil {
		t.Fatal(err)
	}
	record := result.Items[0]
	if record.Decision.Verdict != model.TopicRelevant || record.Preview == nil || record.Preview.Verdict != model.TopicIrrelevant ||
		record.Editor == nil || *record.Editor != model.TopicInsufficient {
		t.Fatalf("preview and review not isolated: %+v", record)
	}
	posts, err := store.ListPublications(ctx, channel.ID, 10, 0)
	if err != nil || len(posts) != 1 {
		t.Fatalf("publication queue changed by historical review: %v (%v)", posts, err)
	}
}

type fixtureFeed struct{ material model.Material }

func (feed *fixtureFeed) Fetch(_ context.Context, source model.Source) ([]model.Material, error) {
	if source.Status != model.SourceVerified {
		return nil, fmt.Errorf("unexpected source status %s", source.Status)
	}
	return []model.Material{feed.material}, nil
}

type recordedTelegram struct{ replies []string }

func (telegram *recordedTelegram) Send(_ context.Context, _ model.Publication, previous string) (string, error) {
	telegram.replies = append(telegram.replies, previous)
	return strconv.Itoa(len(telegram.replies)), nil
}

func requestJSON(t *testing.T, handler http.Handler, method, path, body string, want int) []byte {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, want, response.Body.String())
	}
	return response.Body.Bytes()
}

func pipelineStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := os.Getenv("SMM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SMM_TEST_DATABASE_URL to run the PostgreSQL pipeline")
	}
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("smm_pipeline_%d", time.Now().UnixNano())}.Sanitize()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "drop schema "+schema+" cascade"); err != nil {
			t.Error(err)
		}
	})
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	query.Set("search_path", strings.Trim(schema, "\""))
	address.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, address.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}
