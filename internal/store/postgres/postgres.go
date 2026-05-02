// Package postgres is the Postgres-backed implementation of every repository
// port. Same contract as memory + sqlite; verified by the shared suite when
// OLYMP_TEST_PG_DSN is set.
package postgres

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

	"github.com/lib/pq"
	_ "github.com/lib/pq" // database/sql driver: postgres
)

//go:embed migrations/001_initial.sql
var schemaSQL string

// Open initialises a Postgres-backed ports.Repos at the given DSN. The schema
// is applied idempotently.
func Open(ctx context.Context, dsn string) (ports.Repos, error) {
	if dsn == "" {
		return ports.Repos{}, fmt.Errorf("postgres: dsn is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return ports.Repos{}, fmt.Errorf("postgres: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return ports.Repos{}, fmt.Errorf("postgres: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return ports.Repos{}, fmt.Errorf("postgres: migrate: %w", err)
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

func encodeJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

func decodeJSON(raw []byte, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dst)
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
	var completedAt sql.NullTime
	if run.CompletedAt != nil {
		completedAt = sql.NullTime{Time: run.CompletedAt.UTC(), Valid: true}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Postgres column is `scope text[] not null default '{}'`. pq.Array
	// of a nil slice serialises to NULL, which trips the not-null
	// constraint. Normalise nil → empty slice so the default makes sense.
	scope := run.Scope
	if scope == nil {
		scope = []string{}
	}

	_, err = tx.ExecContext(ctx, `
		insert into runs
		  (id, tenant_org, tenant_team, tenant_user,
		   intent_type, intent_payload, intent_subject, session_id,
		   caller_type, caller_id, caller_name, scope, status, iteration,
		   goal, last_error, pending_decision, started_at, updated_at, completed_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
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
		run.Intent.Type, payload, nullString(run.Intent.Subject), run.Session.ID,
		run.Caller.Type, run.Caller.ID, nullString(run.Caller.Name), pq.Array(scope),
		string(run.Status), run.Iteration, goal, lastErr, pendingDecision,
		run.StartedAt.UTC(), run.UpdatedAt.UTC(), completedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `delete from provenance_steps where run_id = $1`, run.ID); err != nil {
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
		from runs where id = $1`, id)
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
		`update runs set status = $1, updated_at = $2 where id = $3`,
		string(status), time.Now().UTC(), id)
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
	if err := tx.QueryRowContext(ctx, `select 1 from runs where id = $1`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("run %q: %w", id, ports.ErrNotFound)
		}
		return err
	}
	if err := insertStep(ctx, tx, id, step); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `update runs set updated_at = $1 where id = $2`,
		step.CompletedAt.UTC(), id); err != nil {
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
	add := func(c string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf("%s = $%d", c, len(args)))
	}
	if filter.RunID != "" {
		add("id", filter.RunID)
	}
	if filter.IntentType != "" {
		add("intent_type", filter.IntentType)
	}
	if filter.CallerType != "" {
		add("caller_type", filter.CallerType)
	}
	if filter.CallerID != "" {
		add("caller_id", filter.CallerID)
	}
	if filter.Status != "" {
		add("status", string(filter.Status))
	}
	if !filter.Tenant.IsZero() {
		add("tenant_org", filter.Tenant.Org)
		add("tenant_team", filter.Tenant.Team)
		add("tenant_user", filter.Tenant.User)
	}
	if len(conds) > 0 {
		q += " where " + strings.Join(conds, " and ")
	}
	q += " order by started_at desc"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		q += fmt.Sprintf(" limit $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
		payload, goal, lastErr            []byte
		pendingDecision                   []byte
		intentSubject, callerName         sql.NullString
		startedAt, updated                time.Time
		completedAt                       sql.NullTime
		status                            string
		scope                             []string
		tenantOrg, tenantTeam, tenantUser string
	)
	err := s.Scan(
		&run.ID, &tenantOrg, &tenantTeam, &tenantUser,
		&run.Intent.Type, &payload, &intentSubject, &run.Session.ID,
		&run.Caller.Type, &run.Caller.ID, &callerName, pq.Array(&scope), &status, &run.Iteration,
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
	run.Scope = scope
	run.Status = domain.RunStatus(status)
	if err := decodeJSON(payload, &run.Intent.Payload); err != nil {
		return domain.Run{}, err
	}
	if err := decodeJSON(goal, &run.Goal); err != nil {
		return domain.Run{}, err
	}
	if len(lastErr) > 0 && string(lastErr) != "null" {
		var re domain.RunError
		if err := decodeJSON(lastErr, &re); err == nil && re.Code != "" {
			run.LastError = &re
		}
	}
	if len(pendingDecision) > 0 && string(pendingDecision) != "null" {
		var pd domain.DecisionRef
		if err := decodeJSON(pendingDecision, &pd); err == nil && pd.ID != "" {
			run.PendingDecision = &pd
		}
	}
	run.StartedAt = startedAt
	run.UpdatedAt = updated
	if completedAt.Valid {
		t := completedAt.Time
		run.CompletedAt = &t
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
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict(id) do update set
		  inputs=excluded.inputs,
		  outputs=excluded.outputs,
		  error=excluded.error,
		  completed_at=excluded.completed_at
	`,
		id, runID, step.Iteration, string(step.Stage), step.LayerRef.Layer, nullString(step.LayerRef.ID),
		inputs, outputs, stepErr,
		step.StartedAt.UTC(), step.CompletedAt.UTC(),
	)
	return err
}

func loadProvenance(ctx context.Context, db *sql.DB, runID string) ([]domain.ProvenanceStep, error) {
	rows, err := db.QueryContext(ctx, `
		select iteration, stage, layer, layer_ref, inputs, outputs, error, started_at, completed_at
		from provenance_steps where run_id = $1
		order by iteration, started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var steps []domain.ProvenanceStep
	for rows.Next() {
		var (
			step                    domain.ProvenanceStep
			stage, layer            string
			layerRef                sql.NullString
			inputs, outputs, errStr []byte
			startedAt, completedAt  time.Time
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
		if len(errStr) > 0 && string(errStr) != "null" {
			var re domain.RunError
			if err := decodeJSON(errStr, &re); err == nil && re.Code != "" {
				step.Error = &re
			}
		}
		step.StartedAt = startedAt
		step.CompletedAt = completedAt
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
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
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		on conflict(id) do update set
		  tenant_org=excluded.tenant_org,
		  tenant_team=excluded.tenant_team,
		  tenant_user=excluded.tenant_user,
		  caller_type=excluded.caller_type,
		  caller_id=excluded.caller_id,
		  metadata=excluded.metadata,
		  updated_at=excluded.updated_at
	`, s.ID, s.Tenant.Org, s.Tenant.Team, s.Tenant.User, s.Caller.Type, s.Caller.ID, meta, s.CreatedAt.UTC(), s.UpdatedAt.UTC())
	return err
}

func (r *sessionRepo) Get(ctx context.Context, id string) (domain.Session, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, tenant_org, tenant_team, tenant_user, caller_type, caller_id, metadata, created_at, updated_at
		from sessions where id = $1`, id)
	var (
		s                  domain.Session
		meta               []byte
		created, upd       time.Time
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
	s.CreatedAt = created
	s.UpdatedAt = upd
	return s, nil
}

func (r *sessionRepo) List(ctx context.Context, filter domain.SessionFilter) ([]domain.Session, error) {
	q := `select id, tenant_org, tenant_team, tenant_user, caller_type, caller_id, metadata, created_at, updated_at from sessions`
	var (
		conds []string
		args  []any
	)
	add := func(c string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf("%s = $%d", c, len(args)))
	}
	if filter.CallerType != "" {
		add("caller_type", filter.CallerType)
	}
	if filter.CallerID != "" {
		add("caller_id", filter.CallerID)
	}
	if !filter.Tenant.IsZero() {
		add("tenant_org", filter.Tenant.Org)
		add("tenant_team", filter.Tenant.Team)
		add("tenant_user", filter.Tenant.User)
	}
	if len(conds) > 0 {
		q += " where " + strings.Join(conds, " and ")
	}
	q += " order by created_at desc"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		q += fmt.Sprintf(" limit $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Session
	for rows.Next() {
		var (
			s                  domain.Session
			meta               []byte
			created, upd       time.Time
			torg, tteam, tuser string
		)
		if err := rows.Scan(&s.ID, &torg, &tteam, &tuser, &s.Caller.Type, &s.Caller.ID, &meta, &created, &upd); err != nil {
			return nil, err
		}
		s.Tenant = domain.Tenant{Org: torg, Team: tteam, User: tuser}
		_ = decodeJSON(meta, &s.Metadata)
		s.CreatedAt = created
		s.UpdatedAt = upd
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
		values ($1,$2,$3,$4,$5)
		on conflict(name) do update set
		  description=excluded.description,
		  schema=excluded.schema,
		  policy=excluded.policy,
		  registered_at=excluded.registered_at
	`, t.Name, nullString(t.Description), schema, policy, t.RegisteredAt.UTC())
	return err
}

func (r *intentTypeRepo) Get(ctx context.Context, name string) (domain.IntentType, error) {
	row := r.db.QueryRowContext(ctx, `
		select name, description, schema, policy, registered_at
		from intent_types where name = $1`, name)
	var (
		t              domain.IntentType
		desc           sql.NullString
		schema, policy []byte
		regAt          time.Time
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
	t.RegisteredAt = regAt
	return t, nil
}

func (r *intentTypeRepo) List(ctx context.Context) ([]domain.IntentType, error) {
	rows, err := r.db.QueryContext(ctx, `
		select name, description, schema, policy, registered_at
		from intent_types order by name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.IntentType
	for rows.Next() {
		var (
			t              domain.IntentType
			desc           sql.NullString
			schema, policy []byte
			regAt          time.Time
		)
		if err := rows.Scan(&t.Name, &desc, &schema, &policy, &regAt); err != nil {
			return nil, err
		}
		if desc.Valid {
			t.Description = desc.String
		}
		_ = decodeJSON(schema, &t.Schema)
		_ = decodeJSON(policy, &t.Policy)
		t.RegisteredAt = regAt
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
		values ($1,$2,$3,$4,$5)
		on conflict(id) do nothing
	`, e.ID, e.RunID, e.Kind, detail, e.CreatedAt.UTC())
	return err
}

func (r *auditRepo) ListForRun(ctx context.Context, runID string) ([]domain.AuditEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`select id, run_id, kind, detail, created_at from audit_events where run_id = $1 order by created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectAudit(rows)
}

func (r *auditRepo) Search(ctx context.Context, q domain.AuditQuery) ([]domain.AuditEvent, error) {
	query := `select id, run_id, kind, detail, created_at from audit_events`
	var (
		conds []string
		args  []any
	)
	add := func(c, op string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf("%s %s $%d", c, op, len(args)))
	}
	if q.RunID != "" {
		add("run_id", "=", q.RunID)
	}
	if q.Kind != "" {
		add("kind", "=", q.Kind)
	}
	if !q.Since.IsZero() {
		add("created_at", ">=", q.Since.UTC())
	}
	if !q.Until.IsZero() {
		add("created_at", "<=", q.Until.UTC())
	}
	if len(conds) > 0 {
		query += " where " + strings.Join(conds, " and ")
	}
	query += " order by created_at desc"
	if q.Limit > 0 {
		args = append(args, q.Limit)
		query += fmt.Sprintf(" limit $%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out, err := collectAudit(rows)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func collectAudit(rows *sql.Rows) ([]domain.AuditEvent, error) {
	var out []domain.AuditEvent
	for rows.Next() {
		var (
			e         domain.AuditEvent
			detail    []byte
			createdAt time.Time
		)
		if err := rows.Scan(&e.ID, &e.RunID, &e.Kind, &detail, &createdAt); err != nil {
			return nil, err
		}
		_ = decodeJSON(detail, &e.Detail)
		e.CreatedAt = createdAt
		out = append(out, e)
	}
	return out, rows.Err()
}

type approvalRepo struct{ db *sql.DB }

func (r *approvalRepo) Pending(ctx context.Context, runID string) (*domain.ApprovalRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		select run_id, decision_id, required_at, reason
		from approvals where run_id = $1 and resolved_at is null`, runID)
	var (
		req    domain.ApprovalRequest
		reqAt  time.Time
		reason sql.NullString
	)
	err := row.Scan(&req.RunID, &req.DecisionID, &reqAt, &reason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
		}
		return nil, err
	}
	req.RequiredAt = reqAt
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
		values ($1,$2,$3,$4)
		on conflict(run_id) do update set
		  decision_id=excluded.decision_id,
		  required_at=excluded.required_at,
		  reason=excluded.reason,
		  resolved_at=null,
		  decision=null,
		  resolver=null
	`, runID, req.DecisionID, req.RequiredAt.UTC(), nullString(req.Reason))
	return err
}

func (r *approvalRepo) Resolve(ctx context.Context, runID string, decision domain.ApprovalDecision) error {
	resolver, err := encodeJSON(decision.Resolver)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		update approvals set decision = $1, resolver = $2, resolved_at = $3
		where run_id = $4 and resolved_at is null
	`, decision.Decision, resolver, decision.ResolvedAt.UTC(), runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("approval %q: %w", runID, ports.ErrNotFound)
	}
	return nil
}
