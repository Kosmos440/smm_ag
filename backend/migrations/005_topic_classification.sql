alter table material_evaluations add column topic_decision jsonb
    check (jsonb_typeof(topic_decision) = 'object');

create index material_evaluations_recent_idx
    on material_evaluations (channel_id, evaluated_at desc, material_id desc);

create table material_articles (
    material_id bigint primary key references materials(id) on delete cascade,
    state text not null default 'pending' check (state in ('pending', 'ready', 'unavailable')),
    attempts integer not null default 0 check (attempts between 0 and 2),
    next_attempt_at timestamptz not null default now(),
    expires_at timestamptz not null,
    body text not null default '',
    lead text not null default '',
    media jsonb not null default '[]' check (jsonb_typeof(media) = 'array'),
    check ((state = 'ready') = (body <> ''))
);
create index material_articles_pending_idx
    on material_articles (next_attempt_at, material_id) where state = 'pending';

create table topic_embeddings (
    provider_model text not null,
    text_hash bytea not null check (octet_length(text_hash) = 32),
    embedding real[] not null check (cardinality(embedding) between 1 and 4096),
    primary key (provider_model, text_hash)
);

create table topic_reviews (
    channel_id bigint not null,
    material_id bigint not null,
    preview jsonb check (jsonb_typeof(preview) = 'object'),
    editor_verdict text check (editor_verdict in ('relevant', 'irrelevant', 'insufficient')),
    primary key (channel_id, material_id),
    foreign key (channel_id, material_id)
        references material_evaluations(channel_id, material_id) on delete cascade
);
