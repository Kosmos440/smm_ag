package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"smmagent/internal/model"
)

const publicationColumns = `id, channel_id, event_id, version, target, destination, state,
	priority, urgent, title, body, source_url, reason, external_id, last_error, created_at, expires_at, sent_at, media`

func scanPublication(row scanner) (model.Publication, error) {
	var p model.Publication
	var sentAt *time.Time
	err := row.Scan(&p.ID, &p.ChannelID, &p.EventID, &p.Version, &p.Target, &p.Destination,
		&p.State, &p.Priority, &p.Urgent, &p.Title, &p.Body, &p.SourceURL, &p.Reason, &p.ExternalID,
		&p.LastError, &p.CreatedAt, &p.ExpiresAt, &sentAt, &p.Media)
	if sentAt != nil {
		p.SentAt = *sentAt
	}
	return p, storeError(err)
}

func enqueuePublications(ctx context.Context, tx pgx.Tx, channel model.Channel, material model.Material, evaluation model.Evaluation, event model.Event, now time.Time) error {
	state := model.PublicationPending
	if channel.Settings.RequireApproval {
		state = model.PublicationReview
	}
	publishedAt := material.PublishedAt
	if publishedAt.IsZero() {
		publishedAt = material.FetchedAt
	}
	expiresAt := publishedAt.Add(time.Duration(channel.Settings.MaxAgeMinutes) * time.Minute)
	type deliveryTarget struct {
		kind        model.Target
		destination string
	}
	targets := make([]deliveryTarget, 0, 2)
	if channel.TelegramChatID != "" {
		targets = append(targets, deliveryTarget{kind: model.TargetTelegram, destination: channel.TelegramChatID})
	}
	if channel.Website {
		targets = append(targets, deliveryTarget{kind: model.TargetWebsite})
	}
	for _, target := range targets {
		media := material.Media
		if media == nil {
			media = []model.Media{}
		}
		_, err := tx.Exec(ctx, `with previous as (
			update publications set state = 'expired', last_error = 'superseded by a newer event version'
			where event_id = $2 and target = $4 and destination = $5 and state in ('pending', 'review')
			returning priority, urgent, expires_at
		)
		insert into publications (
			channel_id, event_id, version, target, destination, state, priority, urgent,
			title, body, source_url, reason, created_at, expires_at, media
		) values (
			$1, $2, $3, $4, $5, $6,
			greatest($7::integer, (select max(priority) from previous where expires_at > $13)),
			$8::boolean or coalesce((select bool_or(urgent) from previous where expires_at > $13), false),
			$9, $10, $11, $12, $13, $14, $15
		)`, channel.ID, event.ID, event.Version, target.kind, target.destination, state, evaluation.Priority, evaluation.Urgent,
			event.Title, event.Summary, material.URL, evaluation.Reason, now, expiresAt, media)
		if err != nil {
			return storeError(err)
		}
	}
	return nil
}

func (s *Store) ListPublications(ctx context.Context, channelID int64, limit, offset int) ([]model.Publication, error) {
	return s.queryPublications(ctx, `where channel_id = $1 order by id desc limit $2 offset $3`, channelID, limit, offset)
}

func (s *Store) ListReview(ctx context.Context, limit, offset int) ([]model.Publication, error) {
	return s.queryPublications(ctx, `where state = 'review' and expires_at > now() order by id limit $1 offset $2`, limit, offset)
}

func (s *Store) ListSiteNews(ctx context.Context, channelID int64, limit, offset int) ([]model.Publication, error) {
	return s.queryPublications(ctx, `where target = 'website' and state = 'sent'
		and ($1::bigint = 0 or channel_id = $1) order by sent_at desc, id desc limit $2 offset $3`, channelID, limit, offset)
}

func (s *Store) queryPublications(ctx context.Context, where string, args ...any) ([]model.Publication, error) {
	rows, err := s.pool.Query(ctx, "select "+publicationColumns+" from publications "+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Publication, error) { return scanPublication(row) })
}

func (s *Store) DecideReview(ctx context.Context, id int64, approved bool) (model.Publication, error) {
	state := model.PublicationRejected
	if approved {
		state = model.PublicationPending
	}
	p, err := scanPublication(s.pool.QueryRow(ctx, `update publications set state = $2, review_decision = $3
		where id = $1 and state = 'review' and expires_at > now() returning `+publicationColumns, id, state, approved))
	if errors.Is(err, model.ErrNotFound) {
		p, err = scanPublication(s.pool.QueryRow(ctx, "select "+publicationColumns+
			" from publications where id = $1 and review_decision = $2", id, approved))
		if !errors.Is(err, model.ErrNotFound) {
			return p, err
		}
		var exists bool
		err = s.pool.QueryRow(ctx, "select exists (select 1 from publications where id = $1)", id).Scan(&exists)
		if err != nil {
			return model.Publication{}, err
		}
		if exists {
			err = model.ErrConflict
		} else {
			err = model.ErrNotFound
		}
	}
	return p, err
}

func (s *Store) LastSent(ctx context.Context, channelID int64, target model.Target, destination string) (time.Time, error) {
	var sentAt time.Time
	err := s.pool.QueryRow(ctx, `select sent_at from publications
		where channel_id = $1 and target = $2 and destination = $3 and state = 'sent'
		order by sent_at desc limit 1`, channelID, target, destination).Scan(&sentAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return sentAt, err
}

func (s *Store) ClaimPublication(ctx context.Context, channelID int64, target model.Target, destination string, allowUrgent, allowReserve bool, now, reserveBefore time.Time) (model.Publication, error) {
	return scanPublication(s.pool.QueryRow(ctx, `update publications set state = 'sending'
		where id = (
			select p.id from publications p
			join channels c on c.id = p.channel_id and c.enabled
			cross join lateral (
				select p.urgent or (p.version > 1 and exists (
					select 1 from publications previous
					where previous.event_id = p.event_id and previous.target = p.target
						and previous.destination = p.destination and previous.version < p.version
						and previous.state = 'sent'
				)) as immediate
			) delivery
			where p.channel_id = $1 and p.target = $2 and p.destination = $3
			and p.state = 'pending' and p.expires_at > $6
			and (not (c.settings @> '{"require_approval":true}'::jsonb) or p.review_decision is true)
			and ((delivery.immediate and $4) or (not delivery.immediate and $5 and p.created_at <= $7))
			and ((p.target = 'website' and c.website) or (p.target = 'telegram' and p.destination = c.telegram_chat_id))
			and not exists (
				select 1 from publications ongoing
				where ongoing.event_id = p.event_id and ongoing.target = p.target and ongoing.destination = p.destination
				and ongoing.state in ('sending', 'uncertain')
			)
			order by delivery.immediate desc, p.priority desc, p.id limit 1 for update of p skip locked
		)
		returning `+publicationColumns, channelID, target, destination, allowUrgent, allowReserve, now, reserveBefore))
}

func (s *Store) PreviousExternalID(ctx context.Context, p model.Publication) (string, error) {
	var externalID string
	err := s.pool.QueryRow(ctx, `select external_id from publications
		where event_id = $1 and target = $2 and destination = $3 and version < $4 and state = 'sent'
		and external_id <> '' order by version desc limit 1`, p.EventID, p.Target, p.Destination, p.Version).Scan(&externalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return externalID, err
}

func (s *Store) FinishPublication(ctx context.Context, id int64, state model.PublicationState, externalID, errorMessage string, now time.Time) error {
	if state != model.PublicationSent && state != model.PublicationFailed && state != model.PublicationUncertain &&
		state != model.PublicationPending {
		return errors.New("invalid publication delivery result")
	}
	tag, err := s.pool.Exec(ctx, `update publications set state = $2, external_id = $3, last_error = $4,
		sent_at = case when $2 = 'sent' then $5::timestamptz else null end
		where id = $1 and state = 'sending'`, id, state, externalID, errorMessage, now)
	if err == nil && tag.RowsAffected() != 1 {
		return model.ErrConflict
	}
	return err
}

func (s *Store) ExpirePublications(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `update publications p set state = 'expired',
		last_error = case when p.expires_at <= $1 then 'publication age exceeded' else 'destination changed' end
		from channels c where p.channel_id = c.id and p.state in ('pending', 'review')
		and (p.expires_at <= $1 or (p.target = 'website' and not c.website)
			or (p.target = 'telegram' and p.destination <> c.telegram_chat_id))`, now)
	return err
}

func (s *Store) MarkInterruptedPublications(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `update publications set
		state = case when target = 'telegram' then 'uncertain' else 'pending' end,
		last_error = 'worker stopped during delivery' where state = 'sending'`)
	return err
}
