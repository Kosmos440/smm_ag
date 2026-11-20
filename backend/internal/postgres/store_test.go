package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smmagent/internal/model"
)

func TestDurablePublicationLifecycle(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	if err := store.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	source, err := store.CreateSource(ctx, model.Source{
		Name: "Publisher", Organization: "Example", URL: "https://example.org/rss",
		Kind: model.SourceRSS, Status: model.SourceVerified, Primary: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := store.CreateChannel(ctx, model.Channel{
		Name: "Channel", TelegramChatID: "-1234", Website: true, Enabled: true,
		Settings: model.Settings{Topic: "News", MaxAgeMinutes: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	material := model.Material{
		Media:      []model.Media{{URL: "https://example.org/photo.jpg", Kind: "photo"}},
		ExternalID: "notice-1", URL: "https://example.org/notice-1", Title: "Public notice",
		Body: "First version", PublishedAt: now.Add(-time.Minute), FetchedAt: now,
	}
	if err := store.SaveMaterials(ctx, source.ID, []model.Material{material, material}); err != nil {
		t.Fatal(err)
	}
	items, err := store.UnprocessedMaterials(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("duplicate inserted or material unavailable: %d, %v", len(items), err)
	}
	if len(items[0].Material.Media) != 1 {
		t.Fatal("material attachment was lost")
	}
	evaluation := model.Evaluation{
		Event:    model.Event{Title: material.Title, Summary: material.Body},
		Accepted: true, Changed: true, Urgent: true, Priority: 10,
	}
	if err := store.ApplyEvaluation(ctx, channel, items[0].Material, evaluation, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyEvaluation(ctx, channel, items[0].Material, evaluation, now); err != nil {
		t.Fatalf("evaluation replay: %v", err)
	}
	channel.Settings.RequireApproval = true
	channel, err = store.UpdateChannel(ctx, channel.ID, channel)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ClaimPublication(ctx, channel.ID, model.TargetTelegram, channel.TelegramChatID, true, true, now, now)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("claim before review = %v", err)
	}
	approved := approveAll(t, store)
	if len(approved) != 2 {
		t.Fatalf("wanted two separate reviews, got %d", len(approved))
	}
	if _, err := store.DecideReview(ctx, approved[0].ID, true); err != nil {
		t.Fatalf("repeat same review decision: %v", err)
	}
	// Simulate an approved queue snapshot prepared by the older worker.
	if _, err := store.pool.Exec(ctx, "update publications set body = body || ' Читать далее' where channel_id = $1", channel.ID); err != nil {
		t.Fatal(err)
	}
	website, err := store.ClaimPublication(ctx, channel.ID, model.TargetWebsite, "", true, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishPublication(ctx, website.ID, model.PublicationSent, "", "", now); err != nil {
		t.Fatal(err)
	}
	telegram, err := store.ClaimPublication(ctx, channel.ID, model.TargetTelegram, channel.TelegramChatID, true, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(telegram.Media) != 1 {
		t.Fatal("publication attachment was lost")
	}
	if err := store.FinishPublication(ctx, telegram.ID, model.PublicationSent, "41", "", now); err != nil {
		t.Fatal(err)
	}
	feed, err := store.ListSiteNews(ctx, 0, 10, 0)
	if err != nil || len(feed) != 1 || feed[0].ID != website.ID || feed[0].Body != material.Body || telegram.Body != material.Body {
		t.Fatalf("website feed = %+v, %v", feed, err)
	}

	now = now.Add(time.Minute)
	material.Body = "Material changed while retaining the original feed ID"
	material.FetchedAt = now
	if err := store.SaveMaterials(ctx, source.ID, []model.Material{material}); err != nil {
		t.Fatal(err)
	}
	items, err = store.UnprocessedMaterials(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("revision not retained: %d, %v", len(items), err)
	}
	events, err := store.CandidateEvents(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("existing event lookup: %d, %v", len(events), err)
	}
	evaluation.Event = events[0]
	evaluation.Event.Summary = material.Body
	evaluation.Urgent = false
	staleSettings := channel
	channel.Settings.BlockedTerms = []string{"blocked"}
	channel, err = store.UpdateChannel(ctx, channel.ID, channel)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyEvaluation(ctx, staleSettings, items[0].Material, evaluation, now); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("accepted evaluation made with stale settings: %v", err)
	}
	items, err = store.UnprocessedMaterials(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("conflicting evaluation consumed the material: %d, %v", len(items), err)
	}
	if err := store.ApplyEvaluation(ctx, channel, items[0].Material, evaluation, now); err != nil {
		t.Fatal(err)
	}
	approveAll(t, store)
	update, err := store.ClaimPublication(ctx, channel.ID, model.TargetTelegram, channel.TelegramChatID, true, false, now, now.Add(-time.Hour))
	if err != nil || update.Version != 2 || update.Body != material.Body {
		t.Fatalf("new version = %+v, %v", update, err)
	}
	previous, err := store.PreviousExternalID(ctx, update)
	if err != nil || previous != "41" {
		t.Fatalf("previous external ID = %q, %v", previous, err)
	}
	release, err := store.AcquireWorker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = store.AcquireWorker(ctx)
	if !errors.Is(err, ErrWorkerLocked) {
		t.Fatalf("second worker acquire = %v", err)
	}
	if err := store.MarkInterruptedPublications(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	material.Body = "Third version describes another change"
	material.FetchedAt = now
	if err := store.SaveMaterials(ctx, source.ID, []model.Material{material}); err != nil {
		t.Fatal(err)
	}
	items, err = store.UnprocessedMaterials(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("third revision unavailable: %d, %v", len(items), err)
	}
	events, err = store.CandidateEvents(ctx, channel.ID, now.Add(-time.Hour), 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("event lookup for third revision: %d, %v", len(events), err)
	}
	evaluation.Event = events[0]
	evaluation.Event.Summary = material.Body
	if err := store.ApplyEvaluation(ctx, channel, items[0].Material, evaluation, now); err != nil {
		t.Fatal(err)
	}
	approveAll(t, store)
	_, err = store.ClaimPublication(ctx, channel.ID, model.TargetTelegram, channel.TelegramChatID, true, true, now, now)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("Telegram can send while an earlier outcome is uncertain: %v", err)
	}
	if _, err := store.pool.Exec(ctx, "update publications set body = title || '. Читать далее' where target = 'website' and state = 'pending' and channel_id = $1", channel.ID); err != nil {
		t.Fatal(err)
	}
	website, err = store.ClaimPublication(ctx, channel.ID, model.TargetWebsite, "", true, true, now, now)
	if err != nil || website.Version != 3 || website.State != model.PublicationRejected || website.LastError != "текст повторяет заголовок" {
		t.Fatalf("website did not supersede pending older version independently: %+v, %v", website, err)
	}
	history, err := store.ListPublications(ctx, channel.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, publication := range history {
		if publication.ID == update.ID {
			if publication.State != model.PublicationUncertain {
				t.Fatalf("interrupted send state = %s", publication.State)
			}
			return
		}
	}
	t.Fatal("interrupted send disappeared from history")
}

func approveAll(t *testing.T, store *Store) []model.Publication {
	t.Helper()
	ctx := t.Context()
	review, err := store.ListReview(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, publication := range review {
		if _, err := store.DecideReview(ctx, publication.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	return review
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("SMM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SMM_TEST_DATABASE_URL to run PostgreSQL integration checks")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("smmtest_%d_%d", os.Getpid(), time.Now().UnixNano())
	schemaName := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "create schema "+schemaName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "drop schema "+schemaName+" cascade"); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Store{pool: pool}
}
