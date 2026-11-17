package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"

	"smmagent/internal/model"
)

const eventColumns = "id, channel_id, title, summary, facts, version, first_seen, updated_at"

func (s *Store) SaveMaterials(ctx context.Context, sourceID int64, materials []model.Material) error {
	if len(materials) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, material := range materials {
		externalID := material.ExternalID
		if externalID == "" {
			externalID = material.URL
		}
		if externalID == "" || material.Title == "" || material.FetchedAt.IsZero() {
			return errors.New("material missing identity, title, or fetch time")
		}
		// Retain meaningful edits to a stable feed item while ignoring the poll time.
		payload, err := json.Marshal([3]string{material.URL, material.Title, material.Body})
		if err != nil {
			return err
		}
		contentHash := sha256.Sum256(payload)
		var publishedAt *time.Time
		if !material.PublishedAt.IsZero() {
			publishedAt = &material.PublishedAt
		}
		batch.Queue(`insert into materials
			(source_id, external_id, content_hash, url, title, body, organization, is_primary, published_at, fetched_at)
			select id, $2, $3, $4, $5, $6, organization, is_primary, $7, $8
			from sources where id = $1 and status = 'verified'
			on conflict (source_id, external_id, content_hash) do nothing`,
			sourceID, externalID, contentHash[:], material.URL, material.Title, material.Body, publishedAt, material.FetchedAt)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

func (s *Store) UnprocessedMaterials(ctx context.Context, channelID int64, since time.Time, limit int) ([]model.SourcedMaterial, error) {
	rows, err := s.pool.Query(ctx, `select
		m.id, m.source_id, m.external_id, m.url, m.title, m.body, m.organization, m.is_primary, m.published_at, m.fetched_at,
		s.id, s.name, s.organization, s.url, s.kind, s.status, s.topics, s.is_primary, s.last_success, s.last_error
		from materials m join sources s on s.id = m.source_id
		where s.status = 'verified' and coalesce(m.published_at, m.fetched_at) >= $2
		and not exists (
			select 1 from material_evaluations me where me.channel_id = $1 and me.material_id = m.id
		)
		order by coalesce(m.published_at, m.fetched_at), m.id limit $3`, channelID, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.SourcedMaterial, error) {
		var result model.SourcedMaterial
		m, source := &result.Material, &result.Source
		var publishedAt, lastSuccess *time.Time
		err := row.Scan(&m.ID, &m.SourceID, &m.ExternalID, &m.URL, &m.Title, &m.Body, &m.Organization, &m.Primary, &publishedAt, &m.FetchedAt,
			&source.ID, &source.Name, &source.Organization, &source.URL, &source.Kind, &source.Status, &source.Topics, &source.Primary, &lastSuccess, &source.LastError)
		if publishedAt != nil {
			m.PublishedAt = *publishedAt
		}
		if lastSuccess != nil {
			source.LastSuccess = *lastSuccess
		}
		return result, err
	})
}

func scanEvent(row pgx.CollectableRow) (model.Event, error) {
	var event model.Event
	err := row.Scan(&event.ID, &event.ChannelID, &event.Title, &event.Summary, &event.Facts,
		&event.Version, &event.FirstSeen, &event.UpdatedAt)
	return event, err
}

func (s *Store) ListEvents(ctx context.Context, channelID int64, limit, offset int) ([]model.Event, error) {
	rows, err := s.pool.Query(ctx, "select "+eventColumns+
		" from events where channel_id = $1 order by updated_at desc, id desc limit $2 offset $3", channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanEvent)
}

func (s *Store) CandidateEvents(ctx context.Context, channelID int64, since time.Time, limit int) ([]model.Event, error) {
	rows, err := s.pool.Query(ctx, "select "+eventColumns+
		" from events where channel_id = $1 and updated_at >= $2 order by updated_at desc, id desc limit $3", channelID, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanEvent)
}

func (s *Store) ApplyEvaluation(ctx context.Context, channel model.Channel, material model.Material, evaluation model.Evaluation, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)

	current, err := scanChannel(tx.QueryRow(ctx, "select "+channelColumns+" from channels where id = $1 for update", channel.ID))
	if err != nil {
		return err
	}
	if !current.Enabled || !reflect.DeepEqual(current, channel) {
		return model.ErrConflict
	}
	var alreadyEvaluated bool
	err = tx.QueryRow(ctx, "select exists (select 1 from material_evaluations where channel_id = $1 and material_id = $2)",
		channel.ID, material.ID).Scan(&alreadyEvaluated)
	if err != nil || alreadyEvaluated {
		return err
	}
	var sourceStatus model.SourceStatus
	err = tx.QueryRow(ctx, "select status from sources where id = $1 for share", material.SourceID).Scan(&sourceStatus)
	if err != nil {
		return storeError(err)
	}
	if sourceStatus != model.SourceVerified {
		return nil
	}

	var eventID *int64
	outcome := "rejected"
	if evaluation.Accepted {
		event := evaluation.Event
		if evaluation.Changed {
			outcome = "accepted"
			if err := saveEvent(ctx, tx, channel.ID, &event, now); err != nil {
				return err
			}
			if err := enqueuePublications(ctx, tx, channel, material, evaluation, event, now); err != nil {
				return err
			}
		} else {
			outcome = "duplicate"
		}
		eventID = &event.ID
	}
	if _, err := tx.Exec(ctx, `insert into material_evaluations
		(channel_id, material_id, event_id, outcome, reason, evaluated_at) values ($1, $2, $3, $4, $5, $6)`,
		channel.ID, material.ID, eventID, outcome, evaluation.Reason, now); err != nil {
		return storeError(err)
	}
	return tx.Commit(ctx)
}

func saveEvent(ctx context.Context, tx pgx.Tx, channelID int64, event *model.Event, now time.Time) error {
	if event.Facts == nil {
		event.Facts = []model.Fact{}
	}
	if event.ID == 0 {
		event.Version = 1
		return tx.QueryRow(ctx, `insert into events (channel_id, title, summary, facts, version, first_seen, updated_at)
			values ($1, $2, $3, $4, 1, $5, $5) returning id`, channelID, event.Title, event.Summary, event.Facts, now).Scan(&event.ID)
	}
	err := tx.QueryRow(ctx, `update events set title = $3, summary = $4, facts = $5, version = version + 1, updated_at = $6
		where channel_id = $1 and id = $2 and version = $7 returning version`,
		channelID, event.ID, event.Title, event.Summary, event.Facts, now, event.Version).Scan(&event.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrConflict
	}
	return err
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
