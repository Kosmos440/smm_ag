package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"smmagent/internal/model"
)

// RussianMatches returns column-specific matches in the caller's term order.
// Exact matching for stop words and abbreviations is handled by the classifier.
func (s *Store) RussianMatches(ctx context.Context, title, lead, body string, terms []string) ([][3]bool, error) {
	rows, err := s.pool.Query(ctx, `with document as (
		select to_tsvector('russian', $1) as title, to_tsvector('russian', $2) as lead,
		       to_tsvector('russian', $3) as body
	)
	select title @@ query, lead @@ query, body @@ query
	from document, unnest($4::text[]) with ordinality as term(value, ord)
	cross join lateral phraseto_tsquery('russian', term.value) as q(query)
	order by term.ord`, title, lead, body, terms)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) ([3]bool, error) {
		var match [3]bool
		err := row.Scan(&match[0], &match[1], &match[2])
		return match, err
	})
}

func (s *Store) QueueArticle(ctx context.Context, id int64, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `insert into material_articles (material_id, expires_at) values ($1, $2)
		on conflict (material_id) do update set expires_at = greatest(material_articles.expires_at, excluded.expires_at)
		where material_articles.state = 'pending'`, id, expires)
	return err
}

type ArticleJob struct {
	Material model.Material
	Attempt  int
}

func (s *Store) ClaimArticle(ctx context.Context) (ArticleJob, error) {
	// Finalize abandoned last attempts and expired jobs before taking new work.
	if _, err := s.pool.Exec(ctx, `update material_articles set state = 'unavailable'
		where state = 'pending' and next_attempt_at <= now() and (attempts = 2 or expires_at <= now())`); err != nil {
		return ArticleJob{}, err
	}
	var job ArticleJob
	var published *time.Time
	err := s.pool.QueryRow(ctx, `with claim as (
		select a.material_id from material_articles a join materials m on m.id = a.material_id
		join sources src on src.id = m.source_id and src.status = 'verified'
		where a.state = 'pending' and a.next_attempt_at <= now() and a.expires_at > now() and a.attempts < 2
		order by a.next_attempt_at, a.material_id limit 1 for update of a skip locked
	)
	update material_articles a set attempts = a.attempts + 1, next_attempt_at = now() + interval '45 seconds'
	from claim, materials m where a.material_id = claim.material_id and m.id = a.material_id
	returning m.id, m.url, m.title, m.published_at, a.attempts`).
		Scan(&job.Material.ID, &job.Material.URL, &job.Material.Title, &published, &job.Attempt)
	if published != nil {
		job.Material.PublishedAt = *published
	}
	return job, storeError(err)
}

func (s *Store) FinishArticle(ctx context.Context, job ArticleJob, body, lead string, media []model.Media) error {
	if media == nil {
		media = []model.Media{}
	}
	_, err := s.pool.Exec(ctx, `update material_articles
		set state = case when $3 <> '' then 'ready' when attempts = 2 then 'unavailable' else 'pending' end,
			body = $3, lead = $4, media = $5, next_attempt_at = now() + interval '30 seconds'
		where material_id = $1 and attempts = $2 and state = 'pending'`, job.Material.ID, job.Attempt, body, lead, media)
	return err
}

type Article struct {
	State string
	Body  string
	Lead  string
	Media []model.Media
}

func (s *Store) Article(ctx context.Context, id int64) (Article, error) {
	var article Article
	err := s.pool.QueryRow(ctx, `select case when state = 'pending' and expires_at <= now()
		then 'unavailable' else state end, body, lead, media from material_articles where material_id = $1`, id).
		Scan(&article.State, &article.Body, &article.Lead, &article.Media)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return article, err
}

func (s *Store) Embedding(ctx context.Context, modelKey string, hash []byte) ([]float32, error) {
	var value []float32
	err := s.pool.QueryRow(ctx, "select embedding from topic_embeddings where provider_model = $1 and text_hash = $2",
		modelKey, hash).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return value, err
}

func (s *Store) SaveEmbedding(ctx context.Context, modelKey string, hash []byte, value []float32) error {
	_, err := s.pool.Exec(ctx, `insert into topic_embeddings (provider_model, text_hash, embedding) values ($1, $2, $3)
		on conflict (provider_model, text_hash) do nothing`, modelKey, hash, value)
	return err
}

func (s *Store) ListTopics(ctx context.Context, channelID int64, limit, offset int) ([]model.TopicRecord, error) {
	rows, err := s.pool.Query(ctx, `select m.id, m.title, m.url, e.topic_decision, r.preview, r.editor_verdict, e.evaluated_at
		from material_evaluations e join materials m on m.id = e.material_id
		left join topic_reviews r using (channel_id, material_id)
		where e.channel_id = $1 order by e.evaluated_at desc, e.material_id desc limit $2 offset $3`,
		channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.TopicRecord, error) {
		var record model.TopicRecord
		err := row.Scan(&record.MaterialID, &record.Title, &record.URL, &record.Decision, &record.Preview, &record.Editor, &record.EvaluatedAt)
		return record, err
	})
}

func (s *Store) ReviewTopic(ctx context.Context, channelID, materialID int64, verdict model.TopicVerdict) error {
	return s.saveTopicReview(ctx, channelID, materialID, verdict, nil)
}

func (s *Store) SaveTopicPreview(ctx context.Context, channelID, materialID int64, decision model.TopicDecision) error {
	return s.saveTopicReview(ctx, channelID, materialID, "", &decision)
}

func (s *Store) saveTopicReview(ctx context.Context, channelID, materialID int64, verdict model.TopicVerdict, preview *model.TopicDecision) error {
	tag, err := s.pool.Exec(ctx, `insert into topic_reviews (channel_id, material_id, editor_verdict, preview)
		select channel_id, material_id, nullif($3, ''), $4 from material_evaluations
		where channel_id = $1 and material_id = $2
		on conflict (channel_id, material_id) do update set
		editor_verdict = coalesce(excluded.editor_verdict, topic_reviews.editor_verdict),
		preview = coalesce(excluded.preview, topic_reviews.preview)`, channelID, materialID, verdict, preview)
	if err == nil && tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return err
}

func (s *Store) TopicMaterial(ctx context.Context, channelID, materialID int64) (model.Material, error) {
	var item model.Material
	err := s.pool.QueryRow(ctx, `select m.id, m.title, m.url, m.body
		from material_evaluations e join materials m on m.id = e.material_id
		where e.channel_id = $1 and e.material_id = $2`, channelID, materialID).
		Scan(&item.ID, &item.Title, &item.URL, &item.Body)
	return item, storeError(err)
}
