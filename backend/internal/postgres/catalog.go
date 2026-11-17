package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"smmagent/internal/model"
)

const sourceColumns = "id, name, organization, url, kind, status, topics, is_primary, last_success, last_error"
const channelColumns = "id, name, telegram_chat_id, website, enabled, settings"
const presetColumns = "id, name, settings"

type scanner interface {
	Scan(...any) error
}

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.ErrConflict
	}
	return err
}

func scanSource(row scanner) (model.Source, error) {
	var source model.Source
	var lastSuccess *time.Time
	err := row.Scan(&source.ID, &source.Name, &source.Organization, &source.URL, &source.Kind, &source.Status,
		&source.Topics, &source.Primary, &lastSuccess, &source.LastError)
	if lastSuccess != nil {
		source.LastSuccess = *lastSuccess
	}
	return source, storeError(err)
}

func scanChannel(row scanner) (model.Channel, error) {
	var channel model.Channel
	err := row.Scan(&channel.ID, &channel.Name, &channel.TelegramChatID, &channel.Website, &channel.Enabled, &channel.Settings)
	return channel, storeError(err)
}

func scanPreset(row scanner) (model.Preset, error) {
	var preset model.Preset
	err := row.Scan(&preset.ID, &preset.Name, &preset.Settings)
	return preset, storeError(err)
}

func (s *Store) ListSources(ctx context.Context, limit, offset int) ([]model.Source, error) {
	rows, err := s.pool.Query(ctx, "select "+sourceColumns+" from sources order by id limit $1 offset $2", limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Source, error) { return scanSource(row) })
}

func (s *Store) VerifiedSources(ctx context.Context) ([]model.Source, error) {
	rows, err := s.pool.Query(ctx, "select "+sourceColumns+" from sources where status = 'verified' order by id")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Source, error) { return scanSource(row) })
}

func (s *Store) GetSource(ctx context.Context, id int64) (model.Source, error) {
	return scanSource(s.pool.QueryRow(ctx, "select "+sourceColumns+" from sources where id = $1", id))
}

func (s *Store) CreateSource(ctx context.Context, source model.Source) (model.Source, error) {
	return scanSource(s.pool.QueryRow(ctx, `insert into sources (name, organization, url, kind, status, topics, is_primary)
		values ($1, $2, $3, $4, $5, coalesce($6::text[], '{}'), $7) returning `+sourceColumns,
		source.Name, source.Organization, source.URL, source.Kind, source.Status, source.Topics, source.Primary))
}

func (s *Store) UpdateSource(ctx context.Context, id int64, source model.Source) (model.Source, error) {
	return scanSource(s.pool.QueryRow(ctx, `update sources set name = $2, organization = $3,
		last_success = case when (url, kind) is distinct from ($4::text, $5::text) then null else last_success end,
		last_error = case when (url, kind) is distinct from ($4::text, $5::text) then '' else last_error end,
		url = $4, kind = $5, status = $6, topics = coalesce($7::text[], '{}'), is_primary = $8
		where id = $1 returning `+sourceColumns,
		id, source.Name, source.Organization, source.URL, source.Kind, source.Status, source.Topics, source.Primary))
}

func (s *Store) RecordPoll(ctx context.Context, sourceID int64, success bool, errorMessage string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `update sources set
		last_success = case when $2 then $4 else last_success end,
		last_error = case when $2 then '' else $3 end where id = $1 and status = 'verified'`,
		sourceID, success, errorMessage, now)
	return err
}

func (s *Store) ListChannels(ctx context.Context, limit, offset int) ([]model.Channel, error) {
	rows, err := s.pool.Query(ctx, "select "+channelColumns+" from channels order by id limit $1 offset $2", limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Channel, error) { return scanChannel(row) })
}

func (s *Store) EnabledChannels(ctx context.Context) ([]model.Channel, error) {
	rows, err := s.pool.Query(ctx, "select "+channelColumns+" from channels where enabled order by id")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Channel, error) { return scanChannel(row) })
}

func (s *Store) GetChannel(ctx context.Context, id int64) (model.Channel, error) {
	return scanChannel(s.pool.QueryRow(ctx, "select "+channelColumns+" from channels where id = $1", id))
}

func (s *Store) CreateChannel(ctx context.Context, channel model.Channel) (model.Channel, error) {
	return scanChannel(s.pool.QueryRow(ctx, `insert into channels (name, telegram_chat_id, website, enabled, settings)
		values ($1, $2, $3, $4, $5) returning `+channelColumns,
		channel.Name, channel.TelegramChatID, channel.Website, channel.Enabled, channel.Settings))
}

func (s *Store) UpdateChannel(ctx context.Context, id int64, channel model.Channel) (model.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Channel{}, err
	}
	defer rollback(tx)
	previous, err := scanChannel(tx.QueryRow(ctx, "select "+channelColumns+" from channels where id = $1 for update", id))
	if err != nil {
		return model.Channel{}, err
	}
	updated, err := scanChannel(tx.QueryRow(ctx, `update channels set name = $2, telegram_chat_id = $3,
		website = $4, enabled = $5, settings = $6 where id = $1 returning `+channelColumns,
		id, channel.Name, channel.TelegramChatID, channel.Website, channel.Enabled, channel.Settings))
	if err != nil {
		return model.Channel{}, err
	}
	if channel.Settings.RequireApproval && !previous.Settings.RequireApproval {
		_, err = tx.Exec(ctx, `update publications set state = 'review'
			where channel_id = $1 and state = 'pending' and review_decision is distinct from true`, id)
		if err != nil {
			return model.Channel{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Channel{}, err
	}
	return updated, nil
}

func (s *Store) ListPresets(ctx context.Context, limit, offset int) ([]model.Preset, error) {
	rows, err := s.pool.Query(ctx, "select "+presetColumns+" from presets order by id limit $1 offset $2", limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Preset, error) { return scanPreset(row) })
}

func (s *Store) GetPreset(ctx context.Context, id int64) (model.Preset, error) {
	return scanPreset(s.pool.QueryRow(ctx, "select "+presetColumns+" from presets where id = $1", id))
}

func (s *Store) CreatePreset(ctx context.Context, preset model.Preset) (model.Preset, error) {
	return scanPreset(s.pool.QueryRow(ctx, "insert into presets (name, settings) values ($1, $2) returning "+presetColumns,
		preset.Name, preset.Settings))
}

func (s *Store) UpdatePreset(ctx context.Context, id int64, preset model.Preset) (model.Preset, error) {
	return scanPreset(s.pool.QueryRow(ctx, "update presets set name = $2, settings = $3 where id = $1 returning "+presetColumns,
		id, preset.Name, preset.Settings))
}
