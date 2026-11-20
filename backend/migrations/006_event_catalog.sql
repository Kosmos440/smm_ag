create table catalog_events (
    id bigint generated always as identity primary key,
    scope text not null,
    first_seen timestamptz not null,
    last_seen timestamptz not null,
    unique (scope, id)
);

create index catalog_events_recent_idx on catalog_events (scope, last_seen desc, id desc);

create table catalog_event_versions (
    event_id bigint not null references catalog_events(id),
    version integer not null check (version > 0),
    material_id bigint not null references materials(id),
    signature jsonb not null,
    primary key (event_id, version)
);

create index catalog_event_versions_material_idx on catalog_event_versions(material_id);

create table catalog_event_materials (
    scope text not null,
    material_id bigint not null references materials(id),
    event_id bigint not null,
    version integer not null,
    relation text not null check (relation in ('new', 'repeat', 'update')),
    observed_at timestamptz not null,
    primary key (scope, material_id),
    foreign key (scope, event_id) references catalog_events(scope, id),
    foreign key (event_id, version) references catalog_event_versions(event_id, version)
);

create index catalog_event_materials_event_idx on catalog_event_materials(event_id, version);
create index catalog_event_materials_material_idx on catalog_event_materials(material_id);

create index material_evaluations_catalog_window_idx on material_evaluations(evaluated_at, material_id);
