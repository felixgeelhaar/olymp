-- 001_initial.sql — Olymp SQLite schema (TDD §7.5).

create table if not exists sessions (
  id           text primary key,
  tenant_org   text not null default '',
  tenant_team  text not null default '',
  tenant_user  text not null default '',
  caller_type  text not null,
  caller_id    text not null,
  metadata     text not null default '{}',
  created_at   text not null,
  updated_at   text not null
);

create table if not exists intent_types (
  name          text primary key,
  description   text,
  schema        text not null,
  policy        text not null default '{}',
  registered_at text not null
);

create table if not exists runs (
  id             text primary key,
  tenant_org     text not null default '',
  tenant_team    text not null default '',
  tenant_user    text not null default '',
  intent_type    text not null,
  intent_payload text not null,
  intent_subject text,
  session_id     text not null,
  caller_type    text not null,
  caller_id      text not null,
  caller_name    text,
  scope          text not null default '[]',
  status         text not null,
  iteration      integer not null default 0,
  goal             text not null,
  last_error       text,
  pending_decision text,
  started_at       text not null,
  updated_at     text not null,
  completed_at   text
);

create index if not exists idx_runs_session  on runs (session_id, started_at desc);
create index if not exists idx_runs_status   on runs (status, updated_at desc);
create index if not exists idx_runs_caller   on runs (caller_type, caller_id, started_at desc);
create index if not exists idx_runs_tenant   on runs (tenant_org, tenant_team, tenant_user, started_at desc);

create table if not exists provenance_steps (
  id           text primary key,
  run_id       text not null references runs(id) on delete cascade,
  iteration    integer not null,
  stage        text not null,
  layer        text not null,
  layer_ref    text,
  inputs       text not null default '{}',
  outputs      text not null default '{}',
  error        text,
  started_at   text not null,
  completed_at text
);

create index if not exists idx_prov_run_iter on provenance_steps (run_id, iteration, started_at);
create index if not exists idx_prov_layer    on provenance_steps (layer, started_at desc);

create table if not exists audit_events (
  id         text primary key,
  run_id     text not null,
  kind       text not null,
  detail     text not null,
  created_at text not null
);

create index if not exists idx_audit_run   on audit_events (run_id, created_at);
create index if not exists idx_audit_kind  on audit_events (kind, created_at desc);

create table if not exists approvals (
  run_id       text primary key,
  decision_id  text not null,
  required_at  text not null,
  reason       text,
  resolved_at  text,
  decision     text,
  resolver     text
);
