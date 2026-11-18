alter table materials add column media jsonb not null default '[]'::jsonb
    check (jsonb_typeof(media) = 'array');
alter table publications add column media jsonb not null default '[]'::jsonb
    check (jsonb_typeof(media) = 'array');
