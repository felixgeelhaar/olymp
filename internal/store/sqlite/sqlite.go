// Package sqlite is the SQLite-backed implementation of every repository
// port. Same contract as memory; verified by the shared suite.
package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/ports"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var schemaSQL string

// Open initialises a SQLite-backed ports.Repos at the given DSN. The schema
// is applied idempotently. Use ":memory:" for ephemeral test instances.
func Open(ctx context.Context, dsn string) (ports.Repos, error) {
	if dsn == "" {
		dsn = "olymp.db"
	}
	if !strings.Contains(dsn, "_pragma=") && !strings.Contains(dsn, "?") {
		dsn += "?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return ports.Repos{}, fmt.Errorf("sqlite: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return ports.Repos{}, fmt.Errorf("sqlite: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return ports.Repos{}, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return ports.Repos{
		Runs:        &runRepo{db: db},
		Sessions:    &sessionRepo{db: db},
		IntentTypes: &intentTypeRepo{db: db},
		Audit:       &auditRepo{db: db},
		Approvals:   &approvalRepo{db: db},
		Close:       db.Close,
	}, nil
}

const tsLayout = time.RFC3339Nano

func encodeJSON(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeJSON(s string, dst any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

type runRepo struct{ db *sql.DB }

func (r *runRepo) Save(ctx context.Context, run domain.Run) error {
	if err := run.Validate(); err != nil {
		return err
	}
	payload, err := encodeJSON(run.Intent.Payload)
	if err != nil {
		return err
	}
	scope, err := encodeJSON(run.Scope)
	if err != nil {
		return err
	}
	goal, err := encodeJSON(run.Goal)
	if err != nil {
		return err
	}
	lastErr, err := encodeJSON(run.LastError)
	if err != nil {
		return err
	}
	pendingDecision, err := encodeJSON(run.PendingDecision)
	if err != nil {
		return err
	}
	var completedAt sql.NullString
	if run.CompletedAt != nil {
		completedAt = sql.NullString{String: run.CompletedAt.UTC().Format(tsLayout), Valid: true}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // cleanup on commit success is a no-op

	_, err = tx.ExecContext(ctx, `
		insert into runs
		  (id, tenant_org, tenant_team, tenant_user,
		   intent_type, intent_payload, intent_subject, session_id,
		   caller_type, caller_id, caller_name, scope, status, iteration,
		   goal, last_error, pending_decision, started_at, updated_at, completed_at)
		values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
		  tenant_org=excluded.tenant_org,
		  tenant_team=excluded.tenant_team,
		  tenant_user=excluded.tenant_user,
		  intent_type=excluded.intent_type,
		  intent_payload=excluded.intent_payload,
		  intent_subject=excluded.intent_subject,
		  session_id=excluded.session_id,
		  caller_type=excluded.caller_type,
		  caller_id=excluded.caller_id,
		  caller_name=excluded.caller_name,
		  scope=excluded.scope,
		  status=excluded.status,
		  iteration=excluded.iteration,
		  goal=excluded.goal,
		  last_error=excluded.last_error,
		  pending_decision=excluded.pending_decision,
		  updated_at=excluded.updated_at,
		  completed_at=excluded.completed_at
	`,
		run.ID, run.Tenant.Org, run.Tenant.Team, run.Tenant.User,
		run.Intent.Type, payload, run.Intent.Subject, run.Session.ID,
		run.Caller.Type, run.Caller.ID, run.Caller.Name, scope, string(run.Status), run.Iteration,
		goal, lastErr, pendingDecision, run.StartedAt.UTC().Format(tsLayout), run.UpdatedAt.UTC().Format(tsLayout), completedAt,
	)
	if err != nil {
		return fmt.Errorf("sqlite: save run: %w", err)
	}

	// Replace provenance: simple Phase-1 strategy. Saves are infrequent and
	// always carry the full step history in the domain layer.
	if _, err := tx.ExecContext(ctx, `delete from provenance_steps where run_id = ?`, run.ID); err != nil {
		return err
	}
	for _, step := range run.Provenance.Steps {
		if err := insertStep(ctx, tx, run.ID, step); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *runRepo) Get(ctx context.Context, id string) (domain.Run, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, tenant_org, tenant_team, tenant_user,
		       intent_type, intent_payload, intent_subject, session_id,
		       caller_type, caller_id, caller_name, scope, status, iteration,
		       goal, last_error, pending_decision, started_at, updated_at, completed_at
		from runs where id = ?`, id)
	run, err := scanRun(row)
	if err != nil {
		return domain.Run{}, err
	}
	steps, err := loadProvenance(ctx, r.db, id)
	if err != nil {
		return domain.Run{}, err
	}
	run.Provenance.Steps = steps
	return run, nil
}

func (r *runRepo) UpdateStatus(ctx context.Context, id string, status domain.RunStatus) error {
	res, err := r.db.ExecContext(ctx,
		`update runs set status = ?, updated_at = ? where id = ?`,
		string(status), time.Now().UTC().Format(tsLayout), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
	}
	return nil
}

func (r *runRepo) AppendProvenance(ctx context.Context, id string, step domain.ProvenanceStep) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var exists int
	if err := tx.QueryRowContext(ctx, `select 1 from runs where id = ?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
		}
		return err
	}
	if err := insertStep(ctx, tx, id, step); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update runs set updated_at = ? where id = ?`,
		step.CompletedAt.UTC().Format(tsLayout), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *runRepo) List(ctx context.Context, filter domain.RunFilter) ([]domain.Run, error) {
	q := `select id, tenant_org, tenant_team, tenant_user,
	             intent_type, intent_payload, intent_subject, session_id,
	             caller_type, caller_id, caller_name, scope, status, iteration,
	             goal, last_error, pending_decision, started_at, updated_at, completed_at
	      from runs`
	var (
		conds []string
		args  []any
	)
	if filter.RunID != "" {
		conds = append(conds, "id = ?")
		args = append(args, filter.RunID)
	}
	if filter.IntentType != "" {
		conds = append(conds, "intent_type = ?")
		args = append(args, filter.IntentType)
	}
	if filter.CallerType != "" {
		conds = append(conds, "caller_type = ?")
		args = append(args, filter.CallerType)
	}
	if filter.CallerID != "" {
		conds = append(conds, "caller_id = ?")
		args = append(args, filter.CallerID)
	}
	if filter.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(filter.Status))
	}
	if !filter.Tenant.IsZero() {
		conds = append(conds, "tenant_org = ? and tenant_team = ? and tenant_user = ?")
		args = append(args, filter.Tenant.Org, filter.Tenant.Team, filter.Tenant.User)
	}
	if len(conds) > 0 {
		q += " where " + strings.Join(conds, " and ")
	}
	q += " order by started_at desc"
	if filter.Limit > 0 {
		q += " limit ?"
		args = append(args, filter.Limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(s rowScanner) (domain.Run, error) {
	var (
		run                               domain.Run
		payload, scope                    string
		goal, lastErr                     string
		pendingDecision                   sql.NullString
		intentSubject                     sql.NullString
		callerName                        sql.NullString
		startedAt, updated                string
		completedAt                       sql.NullString
		status                            string
		tenantOrg, tenantTeam, tenantUser string
	)
	err := s.Scan(
		&run.ID, &tenantOrg, &tenantTeam, &tenantUser,
		&run.Intent.Type, &payload, &intentSubject, &run.Session.ID,
		&run.Caller.Type, &run.Caller.ID, &callerName, &scope, &status, &run.Iteration,
		&goal, &lastErr, &pendingDecision, &startedAt, &updated, &completedAt,
	)
	run.Tenant = domain.Tenant{Org: tenantOrg, Team: tenantTeam, User: tenantUser}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Run{}, fmt.Errorf("%w", ports.ErrNotFound)
		}
		return domain.Run{}, err
	}
	if intentSubject.Valid {
		run.Intent.Subject = intentSubject.String
	}
	if callerName.Valid {
		run.Caller.Name = callerName.String
	}
	run.Status = domain.RunStatus(status)
	if err := decodeJSON(payload, &run.Intent.Payload); err != nil {
		return domain.Run{}, err
	}
	if err := decodeJSON(scope, &run.Scope); err != nil {
		return domain.Run{}, err
	}
	if err := decodeJSON(goal, &run.Goal); err != nil {
		return domain.Run{}, err
	}
	if lastErr != "" && lastErr != "null" {
		var re domain.RunError
		if err := decodeJSON(lastErr, &re); err == nil && re.Code != "" {
			run.LastError = &re
		}
	}
	if pendingDecision.Valid && pendingDecision.String != "" && pendingDecision.String != "null" {
		var pd domain.DecisionRef
		if err := decodeJSON(pendingDecision.String, &pd); err == nil && pd.ID != "" {
			run.PendingDecision = &pd
		}
	}
	if t, err := time.Parse(tsLayout, startedAt); err == nil {
		run.StartedAt = t
	}
	if t, err := time.Parse(tsLayout, updated); err == nil {
		run.UpdatedAt = t
	}
	if completedAt.Valid {
		if t, err := time.Parse(tsLayout, completedAt.String); err == nil {
			run.CompletedAt = &t
		}
	}
	return run, nil
}

func insertStep(ctx context.Context, tx *sql.Tx, runID string, step domain.ProvenanceStep) error {
	inputs, err := encodeJSON(step.Inputs)
	if err != nil {
		return err
	}
	outputs, err := encodeJSON(step.Outputs)
	if err != nil {
		return err
	}
	stepErr, err := encodeJSON(step.Error)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%s-%d-%s", runID, step.Iteration, step.Stage)
	_, err = tx.ExecContext(ctx, `
		insert into provenance_steps
		  (id, run_id, iteration, stage, layer, layer_ref, inputs, outputs, error, started_at, completed_at)
		values (?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
		  inputs=excluded.inputs,
		  outputs=excluded.outputs,
		  error=excluded.error,
		  completed_at=excluded.completed_at
	`,
		id, runID, step.Iteration, string(step.Stage), step.LayerRef.Layer, step.LayerRef.ID,
		inputs, outputs, stepErr,
		step.StartedAt.UTC().Format(tsLayout), step.CompletedAt.UTC().Format(tsLayout),
	)
	return err
}

func loadProvenance(ctx context.Context, db *sql.DB, runID string) ([]domain.ProvenanceStep, error) {
	rows, err := db.QueryContext(ctx, `
		select iteration, stage, layer, layer_ref, inputs, outputs, error, started_at, completed_at
		from provenance_steps where run_id = ?
		order by iteration, started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []domain.ProvenanceStep
	for rows.Next() {
		var (
			step                    domain.ProvenanceStep
			stage, layer            string
			layerRef                sql.NullString
			inputs, outputs, errStr string
			startedAt, completedAt  string
		)
		if err := rows.Scan(&step.Iteration, &stage, &layer, &layerRef, &inputs, &outputs, &errStr, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		step.Stage = domain.RunStatus(stage)
		step.LayerRef.Layer = layer
		if layerRef.Valid {
			step.LayerRef.ID = layerRef.String
		}
		_ = decodeJSON(inputs, &step.Inputs)
		_ = decodeJSON(outputs, &step.Outputs)
		if errStr != "" && errStr != "null" {
			var re domain.RunError
			if err := decodeJSON(errStr, &re); err == nil && re.Code != "" {
				step.Error = &re
			}
		}
		if t, err := time.Parse(tsLayout, startedAt); err == nil {
			step.StartedAt = t
		}
		if t, err := time.Parse(tsLayout, completedAt); err == nil {
			step.CompletedAt = t
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

type sessionRepo struct{ db *sql.DB }

func (r *sessionRepo) Upsert(ctx context.Context, s domain.Session) error {
	if s.ID == "" {
		return fmt.Errorf("session: id is required")
	}
	meta, err := encodeJSON(s.Metadata)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		insert into sessions (id, tenant_org, tenant_team, tenant_user, caller_type, caller_id, metadata, created_at, updated_at)
		values (?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
		  tenant_org=excluded.tenant_org,
		  tenant_team=excluded.tenant_team,
		  tenant_user=excluded.tenant_user,
		  caller_type=excluded.caller_type,
		  caller_id=excluded.caller_id,
		  metadata=excluded.metadata,
		  updated_at=excluded.updated_at
	`, s.ID, s.Tenant.Org, s.Tenant.Team, s.Tenant.User, s.Caller.Type, s.Caller.ID, meta,
		s.CreatedAt.UTC().Format(tsLayout), s.UpdatedAt.UTC().Format(tsLayout))
	return err
}

func (r *sessionRepo) Get(ctx context.Context, id string) (domain.Session, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, tenant_org, tenant_team, tenant_user, caller_type, caller_id, metadata, created_at, updated_at
		from sessions where id = ?`, id)
	var (
		s                  domain.Session
		meta               string
		created, upd       string
		torg, tteam, tuser string
	)
	err := row.Scan(&s.ID, &torg, &tteam, &tuser, &s.Caller.Type, &s.Caller.ID, &meta, &created, &upd)
	s.Tenant = domain.Tenant{Org: torg, Team: tteam, User: tuser}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, fmt.Errorf("session %q: %w", id, ports.ErrNotFound)
		}
		return domain.Session{}, err
	}
	_ = decodeJSON(meta, &s.Metadata)
	if t, err := time.Parse(tsLayout, created); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(tsLayout, upd); err == nil {
		s.UpdatedAt = t
	}
	return s, nil
}

func (r *sessionRepo) List(ctx context.Context, filter domain.SessionFilter) ([]domain.Session, error) {
	q := `select id, tenant_org, tenant_team, tenant_user, caller_type, caller_id, metadata, created_at, updated_at from sessions`
	var (
		conds []string
		args  []any
	)
	if filter.CallerType != "" {
		conds = append(conds, "caller_type = ?")
		args = append(args, filter.CallerType)
	}
	if filter.CallerID != "" {
		conds = append(conds, "caller_id = ?")
		args = append(args, filter.CallerID)
	}
	if !filter.Tenant.IsZero() {
		conds = append(conds, "tenant_org = ? and tenant_team = ? and tenant_user = ?")
		args = append(args, filter.Tenant.Org, filter.Tenant.Team, filter.Tenant.User)
	}
	if len(conds) > 0 {
		q += " where " + strings.Join(conds, " and ")
	}
	q += " order by created_at desc"
	if filter.Limit > 0 {
		q += " limit ?"
		args = append(args, filter.Limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		var (
			s                  domain.Session
			meta               string
			created            string
			upd                string
			torg, tteam, tuser string
		)
		if err := rows.Scan(&s.ID, &torg, &tteam, &tuser, &s.Caller.Type, &s.Caller.ID, &meta, &created, &upd); err != nil {
			return nil, err
		}
		s.Tenant = domain.Tenant{Org: torg, Team: tteam, User: tuser}
		_ = decodeJSON(meta, &s.Metadata)
		if t, err := time.Parse(tsLayout, created); err == nil {
			s.CreatedAt = t
		}
		if t, err := time.Parse(tsLayout, upd); err == nil {
			s.UpdatedAt = t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type intentTypeRepo struct{ db *sql.DB }

func (r *intentTypeRepo) Register(ctx context.Context, t domain.IntentType) error {
	if t.Name == "" {
		return fmt.Errorf("intent type: name is required")
	}
	schema, err := encodeJSON(t.Schema)
	if err != nil {
		return err
	}
	policy, err := encodeJSON(t.Policy)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		insert into intent_types (name, description, schema, policy, registered_at)
		values (?,?,?,?,?)
		on conflict(name) do update set
		  description=excluded.description,
		  schema=excluded.schema,
		  policy=excluded.policy,
		  registered_at=excluded.registered_at
	`, t.Name, t.Description, schema, policy, t.RegisteredAt.UTC().Format(tsLayout))
	return err
}

func (r *intentTypeRepo) Get(ctx context.Context, name string) (domain.IntentType, error) {
	row := r.db.QueryRowContext(ctx, `
		select name, description, schema, policy, registered_at
		from intent_types where name = ?`, name)
	var (
		t              domain.IntentType
		desc           sql.NullString
		schema, policy string
		regAt          string
	)
	err := row.Scan(&t.Name, &desc, &schema, &policy, &regAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.IntentType{}, fmt.Errorf("intent type %q: %w", name, ports.ErrNotFound)
		}
		return domain.IntentType{}, err
	}
	if desc.Valid {
		t.Description = desc.String
	}
	_ = decodeJSON(schema, &t.Schema)
	_ = decodeJSON(policy, &t.Policy)
	if ts, err := time.Parse(tsLayout, regAt); err == nil {
		t.RegisteredAt = ts
	}
	return t, nil
}

func (r *intentTypeRepo) List(ctx context.Context) ([]domain.IntentType, error) {
	rows, err := r.db.QueryContext(ctx, `
		select name, description, schema, policy, registered_at
		from intent_types order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IntentType
	for rows.Next() {
		var (
			t              domain.IntentType
			desc           sql.NullString
			schema, policy string
			regAt          string
		)
		if err := rows.Scan(&t.Name, &desc, &schema, &policy, &regAt); err != nil {
			return nil, err
		}
		if desc.Valid {
			t.Description = desc.String
		}
		_ = decodeJSON(schema, &t.Schema)
		_ = decodeJSON(policy, &t.Policy)
		if ts, err := time.Parse(tsLayout, regAt); err == nil {
			t.RegisteredAt = ts
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type auditRepo struct{ db *sql.DB }

func (r *auditRepo) Append(ctx context.Context, e domain.AuditEvent) error {
	if e.RunID == "" {
		return fmt.Errorf("audit: run_id is required")
	}
	detail, err := encodeJSON(e.Detail)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		insert into audit_events (id, run_id, kind, detail, created_at)
		values (?,?,?,?,?)
		on conflict(id) do nothing
	`, e.ID, e.RunID, e.Kind, detail, e.CreatedAt.UTC().Format(tsLayout))
	return err
}

func (r *auditRepo) ListForRun(ctx context.Context, runID string) ([]domain.AuditEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`select id, run_id, kind, detail, created_at from audit_events where run_id = ? order by created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAudit(rows)
}

func (r *auditRepo) Search(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	query := `select id, run_id, kind, detail, created_at from audit_events`
	var (
		conds []string
		args  []any
	)
	if q.RunID != "" {
		conds = append(conds, "run_id = ?")
		args = append(args, q.RunID)
	}
	if q.Kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, q.Kind)
	}
	if !q.Since.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, q.Since.UTC().Format(tsLayout))
	}
	if !q.Until.IsZero() {
		conds = append(conds, "created_at <= ?")
		args = append(args, q.Until.UTC().Format(tsLayout))
	}
	if len(conds) > 0 {
		query += " where " + strings.Join(conds, " and ")
	}
	query += " order by created_at desc"
	if q.Limit > 0 {
		query += " limit ?"
		args = append(args, q.Limit)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := collectAudit(rows)
	if err != nil {
		return nil, err
	}
	// stabilise ordering for ties
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func collectAudit(rows *sql.Rows) ([]domain.AuditEvent, error) {
	var out []domain.AuditEvent
	for rows.Next() {
		var (
			e         domain.AuditEvent
			detail    string
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.RunID, &e.Kind, &detail, &createdAt); err != nil {
			return nil, err
		}
		_ = decodeJSON(detail, &e.Detail)
		if t, err := time.Parse(tsLayout, createdAt); err == nil {
			e.CreatedAt = t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type approvalRepo struct{ db *sql.DB }

func (r *approvalRepo) Pending(ctx context.Context, runID string) (*domain.ApprovalRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		select run_id, decision_id, required_at, reason
		from approvals where run_id = ? and resolved_at is null`, runID)
	var (
		req    domain.ApprovalRequest
		reqAt  string
		reason sql.NullString
	)
	err := row.Scan(&req.RunID, &req.DecisionID, &reqAt, &reason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
		}
		return nil, err
	}
	if t, err := time.Parse(tsLayout, reqAt); err == nil {
		req.RequiredAt = t
	}
	if reason.Valid {
		req.Reason = reason.String
	}
	return &req, nil
}

func (r *approvalRepo) Raise(ctx context.Context, runID string, req domain.ApprovalRequest) error {
	if runID == "" {
		return fmt.Errorf("approval: run_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		insert into approvals (run_id, decision_id, required_at, reason)
		values (?,?,?,?)
		on conflict(run_id) do update set
		  decision_id=excluded.decision_id,
		  required_at=excluded.required_at,
		  reason=excluded.reason,
		  resolved_at=null,
		  decision=null,
		  resolver=null
	`, runID, req.DecisionID, req.RequiredAt.UTC().Format(tsLayout), req.Reason)
	return err
}

func (r *approvalRepo) Resolve(ctx context.Context, runID string, decision domain.ApprovalDecision) error {
	resolver, err := encodeJSON(decision.Resolver)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		update approvals set decision = ?, resolver = ?, resolved_at = ?
		where run_id = ? and resolved_at is null
	`, decision.Decision, resolver, decision.ResolvedAt.UTC().Format(tsLayout), runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
	}
	return nil
}
