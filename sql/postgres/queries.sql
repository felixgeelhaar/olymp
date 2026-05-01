-- Olymp Postgres queries (sqlc generation target). Hand-written backend in
-- internal/store/postgres is the canonical implementation today; this file
-- exists so `sqlc generate` produces a typed mirror when we migrate.

-- name: GetRun :one
select id, intent_type, intent_payload, intent_subject, session_id,
       caller_type, caller_id, caller_name, scope, status, iteration,
       goal, last_error, started_at, updated_at, completed_at
from runs where id = $1;

-- name: ListRuns :many
select id, intent_type, intent_payload, intent_subject, session_id,
       caller_type, caller_id, caller_name, scope, status, iteration,
       goal, last_error, started_at, updated_at, completed_at
from runs
order by started_at desc
limit $1;

-- name: UpdateRunStatus :exec
update runs set status = $1, updated_at = $2 where id = $3;

-- name: AppendAuditEvent :exec
insert into audit_events (id, run_id, kind, detail, created_at)
values ($1, $2, $3, $4, $5)
on conflict(id) do nothing;

-- name: ListAuditForRun :many
select id, run_id, kind, detail, created_at
from audit_events
where run_id = $1
order by created_at;
