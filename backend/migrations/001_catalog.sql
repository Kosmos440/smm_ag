create table sources (
    id bigint generated always as identity primary key,
    name text not null check (name <> ''),
    organization text not null check (organization <> ''),
    url text not null,
    kind text not null check (kind in ('rss', 'website', 'telegram')),
    status text not null check (status in ('candidate', 'verified', 'disabled')),
    topics text[] not null default '{}',
    is_primary boolean not null default false,
    last_success timestamptz,
    last_error text not null default '',
    unique (kind, url)
);

create index sources_verified_idx on sources (id) where status = 'verified';

create table channels (
    id bigint generated always as identity primary key,
    name text not null unique check (name <> ''),
    telegram_chat_id text not null default '',
    website boolean not null default false,
    enabled boolean not null default true,
    settings jsonb not null check (jsonb_typeof(settings) = 'object')
);

create table presets (
    id bigint generated always as identity primary key,
    name text not null unique check (name <> ''),
    settings jsonb not null check (jsonb_typeof(settings) = 'object')
);

create table materials (
    id bigint generated always as identity primary key,
    source_id bigint not null references sources (id),
    external_id text not null check (external_id <> ''),
    content_hash bytea not null,
    url text not null,
    title text not null check (title <> ''),
    body text not null,
    organization text not null,
    is_primary boolean not null,
    published_at timestamptz,
    fetched_at timestamptz not null,
    unique (source_id, external_id, content_hash)
);

create index materials_freshness_idx on materials ((coalesce(published_at, fetched_at)), id);

create table events (
    id bigint generated always as identity primary key,
    channel_id bigint not null references channels (id),
    title text not null,
    summary text not null,
    facts jsonb not null check (jsonb_typeof(facts) = 'array'),
    version integer not null check (version > 0),
    first_seen timestamptz not null,
    updated_at timestamptz not null,
    unique (channel_id, id)
);

create index events_channel_updated_idx on events (channel_id, updated_at desc, id desc);

create table material_evaluations (
    channel_id bigint not null references channels (id),
    material_id bigint not null references materials (id),
    event_id bigint,
    outcome text not null check (outcome in ('accepted', 'duplicate', 'rejected')),
    reason text not null,
    evaluated_at timestamptz not null,
    primary key (channel_id, material_id),
    foreign key (channel_id, event_id) references events (channel_id, id),
    check ((event_id is null) = (outcome = 'rejected'))
);

create index material_evaluations_material_idx on material_evaluations (material_id);
create index material_evaluations_event_idx on material_evaluations (channel_id, event_id) where event_id is not null;
