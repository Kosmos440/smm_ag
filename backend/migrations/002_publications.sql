create table publications (
    id bigint generated always as identity primary key,
    channel_id bigint not null,
    event_id bigint not null,
    version integer not null check (version > 0),
    target text not null check (target in ('telegram', 'website')),
    destination text not null default '',
    state text not null check (state in ('review', 'pending', 'sending', 'sent', 'rejected', 'expired', 'uncertain', 'failed')),
    priority integer not null,
    urgent boolean not null,
    title text not null,
    body text not null,
    source_url text not null,
    reason text not null,
    external_id text not null default '',
    last_error text not null default '',
    created_at timestamptz not null,
    expires_at timestamptz not null,
    sent_at timestamptz,
    foreign key (channel_id, event_id) references events (channel_id, id),
    unique (event_id, target, destination, version),
    check ((target = 'website') = (destination = '')),
    check ((state = 'sent') = (sent_at is not null)),
    check (target <> 'telegram' or state <> 'sent' or external_id <> '')
);

create index publications_event_idx on publications (channel_id, event_id);
create index publications_channel_history_idx on publications (channel_id, id desc);
create index publications_queue_idx
    on publications (channel_id, target, destination, urgent desc, priority desc, id)
    where state = 'pending';
create index publications_queue_expiry_idx on publications (expires_at) where state in ('pending', 'review');
create index publications_review_idx on publications (id) where state = 'review';
create index publications_delivery_block_idx
    on publications (event_id, target, destination)
    where state in ('sending', 'uncertain');
create index publications_last_sent_idx
    on publications (channel_id, target, destination, sent_at desc)
    where state = 'sent';
create index publications_site_feed_idx
    on publications (sent_at desc, id desc)
    where target = 'website' and state = 'sent';
