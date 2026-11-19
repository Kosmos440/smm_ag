package postgres

import (
	"context"
	"html"
	"time"

	"github.com/jackc/pgx/v5"

	"smmagent/internal/model"
	"smmagent/internal/news"
)

const catalogLockID int32 = 0x534d4d06

type CatalogMaterial struct {
	model.Material
	ObservedAt time.Time
}

type CatalogAssignment struct {
	MaterialID int64  `json:"material_id"`
	EventID    int64  `json:"event_id"`
	Version    int    `json:"version"`
	Reading    int    `json:"reading,omitempty"`
	Relation   string `json:"relation"`
	Title      string `json:"title"`
}

func (s *Store) CatalogMaterials(ctx context.Context, scope string, channelID int64, start, end time.Time) ([]CatalogMaterial, error) {
	rows, err := s.pool.Query(ctx, `with eligible as (
		select e.material_id, min(e.evaluated_at) as observed_at
		from material_evaluations e
		left join topic_reviews r using (channel_id, material_id)
		where e.evaluated_at >= $3 and e.evaluated_at < $4
		  and ($2::bigint = 0 or e.channel_id = $2)
		  and (e.outcome in ('accepted', 'duplicate') or r.preview->>'verdict' = 'relevant'
		       or r.editor_verdict = 'relevant' or e.topic_decision->>'verdict' = 'relevant')
		  and not exists (select 1 from catalog_event_materials c where c.scope = $1 and c.material_id = e.material_id)
		group by e.material_id order by observed_at, e.material_id limit 100
	)
	select m.id, m.title, coalesce(nullif(a.body, ''), m.body), m.url, e.observed_at
	from eligible e join materials m on m.id = e.material_id
	left join material_articles a on a.material_id = m.id and a.state = 'ready'
	order by e.observed_at, m.id`, scope, channelID, start, end)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CatalogMaterial, error) {
		var item CatalogMaterial
		err := row.Scan(&item.ID, &item.Title, &item.Body, &item.URL, &item.ObservedAt)
		return item, err
	})
}

func (s *Store) CatalogMaterial(ctx context.Context, scope string, item CatalogMaterial) error {
	var lexemes []string
	if err := s.pool.QueryRow(ctx, `
		select tsvector_to_array(to_tsvector('russian', $1))`,
		html.UnescapeString(item.Title)).Scan(&lexemes); err != nil {
		return err
	}
	signature := news.DescribeCatalogMaterial(item.Material, lexemes)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, hashtext(current_schema() || ':' || $2))`, catalogLockID, scope); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(
		select 1 from catalog_event_materials where scope = $1 and material_id = $2)`, scope, item.ID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	rows, err := tx.Query(ctx, `select recent.id, v.version, v.signature
		from (
			select id from catalog_events where scope = $1 and last_seen >= $2
			order by last_seen desc, id desc limit 500
		) recent
		cross join lateral (
			select version, signature from catalog_event_versions
			where event_id = recent.id order by version desc limit 1
		) v`, scope, item.ObservedAt.Add(-72*time.Hour))
	if err != nil {
		return err
	}
	candidates, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (news.CatalogVersion, error) {
		var candidate news.CatalogVersion
		err := row.Scan(&candidate.EventID, &candidate.Version, &candidate.Signature)
		return candidate, err
	})
	if err != nil {
		return err
	}
	event := news.MatchCatalogEvent(signature, candidates)
	relation := "repeat"
	if event.EventID == 0 {
		relation, event.Version = "new", 1
		err = tx.QueryRow(ctx, `insert into catalog_events(scope, first_seen, last_seen)
			values ($1, $2, $2) returning id`, scope, item.ObservedAt).Scan(&event.EventID)
	} else {
		if signature.Reading > event.Signature.Reading {
			relation = "update"
			event.Version++
		} else if signature.Reading > 0 && signature.Reading < event.Signature.Reading {
			err = tx.QueryRow(ctx, `select coalesce(max(version), $3) from catalog_event_versions
				where event_id = $1 and signature->>'reading' = $2::int::text`,
				event.EventID, signature.Reading, event.Version).Scan(&event.Version)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `update catalog_events
			set last_seen = greatest(last_seen, $2), first_seen = least(first_seen, $2)
			where id = $1`, event.EventID, item.ObservedAt)
	}
	if err != nil {
		return err
	}
	if relation != "repeat" {
		if _, err := tx.Exec(ctx, `insert into catalog_event_versions(event_id, version, material_id, signature)
			values ($1, $2, $3, $4)`, event.EventID, event.Version, item.ID, signature); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into catalog_event_materials(scope, material_id, event_id, version, relation, observed_at)
		values ($1, $2, $3, $4, $5, $6)`, scope, item.ID, event.EventID, event.Version, relation, item.ObservedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CatalogAssignments(ctx context.Context, scope string, ids []int64) ([]CatalogAssignment, error) {
	rows, err := s.pool.Query(ctx, `select c.material_id, c.event_id, c.version,
		coalesce((v.signature->>'reading')::int, 0), c.relation, m.title
		from catalog_event_materials c
		join catalog_event_versions v using (event_id, version)
		join materials m on m.id = c.material_id
		where c.scope = $1 and (coalesce(cardinality($2::bigint[]), 0) = 0 or c.material_id = any($2))
		order by c.observed_at, c.material_id`, scope, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CatalogAssignment, error) {
		var rowData CatalogAssignment
		err := row.Scan(&rowData.MaterialID, &rowData.EventID, &rowData.Version, &rowData.Reading, &rowData.Relation, &rowData.Title)
		return rowData, err
	})
}
