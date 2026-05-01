-- Olymp SQLite queries (sqlc generation target). Hand-written backend in
-- internal/store/sqlite is the canonical implementation today; this file
-- exists so `sqlc generate` produces a typed mirror once we choose to migrate.

-- name: GetRun :one
select id, intent_type, intent_payload, intent_subject, session_id,
       caller_type, caller_id, caller_name, scope, status, iteration,
       goal, last_error, started_at, updated_at, completed_at
from runs where id = ?;

-- name: ListRuns :many
select id, intent_type, intent_payload, intent_subject, session_id,
       caller_type, caller_id, caller_name, scope, status, iteration,
       goal, last_error, started_at, updated_at, completed_at
from runs
order by started_at desc
limit ?;

-- name: UpdateRunStatus :exec
update runs set status = ?, updated_at = ? where id = ?;

-- name: AppendAuditEvent :exec
insert into audit_events (id, run_id, kind, detail, created_at)
values (?, ?, ?, ?, ?)
on conflict(id) do nothing;

-- name: ListAuditForRun :many
select id, run_id, kind, detail, created_at
from audit_events
where run_id = ?
order by created_at;
