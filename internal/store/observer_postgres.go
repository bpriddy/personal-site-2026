package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresObserver is the ObserverStore backed by the site's Postgres database
// (tables from migrations/0003_observer.sql).
type PostgresObserver struct {
	pool *pgxpool.Pool
}

// Observer returns the observer store sharing p's connection pool.
func (p *Postgres) Observer() *PostgresObserver { return &PostgresObserver{pool: p.pool} }

const detectionCols = `id, kind, frontend, serve, route, collection, item, field, expect, got,
	signature, count, first_seen, last_seen, sample, status, action, seen_at`

func scanDetection(r pgx.Row) (Detection, error) {
	var d Detection
	var sample, action []byte
	err := r.Scan(&d.ID, &d.Kind, &d.Frontend, &d.Serve, &d.Route, &d.Collection, &d.Item, &d.Field,
		&d.Expect, &d.Got, &d.Signature, &d.Count, &d.FirstSeen, &d.LastSeen, &sample, &d.Status, &action, &d.SeenAt)
	if err != nil {
		return d, notFound(err)
	}
	d.Sample = sample
	if len(action) > 0 {
		if err := json.Unmarshal(action, &d.Action); err != nil {
			return d, err
		}
	}
	return d, nil
}

func (o *PostgresObserver) RecordDetection(ctx context.Context, d Detection) (Detection, bool, error) {
	if len(d.Sample) == 0 {
		d.Sample = json.RawMessage("{}")
	}
	if d.Status == "" {
		d.Status = StatusNew
	}
	action, err := json.Marshal(d.Action)
	if err != nil {
		return d, false, err
	}
	// xmax = 0 only for a freshly inserted row
	var created bool
	row := o.pool.QueryRow(ctx, `
		INSERT INTO detections (kind, frontend, serve, route, collection, item, field, expect, got,
		                        signature, sample, status, action)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (signature) DO UPDATE
		SET count = detections.count + 1, last_seen = now(), sample = excluded.sample,
		    serve = excluded.serve, route = excluded.route, expect = excluded.expect, got = excluded.got
		RETURNING `+detectionCols+`, (xmax = 0)`,
		d.Kind, d.Frontend, d.Serve, d.Route, d.Collection, d.Item, d.Field, d.Expect, d.Got,
		d.Signature, []byte(d.Sample), d.Status, action)
	var out Detection
	var sample, act []byte
	err = row.Scan(&out.ID, &out.Kind, &out.Frontend, &out.Serve, &out.Route, &out.Collection, &out.Item, &out.Field,
		&out.Expect, &out.Got, &out.Signature, &out.Count, &out.FirstSeen, &out.LastSeen, &sample, &out.Status, &act, &out.SeenAt,
		&created)
	if err != nil {
		return out, false, err
	}
	out.Sample = sample
	if err := json.Unmarshal(act, &out.Action); err != nil {
		return out, false, err
	}
	return out, created, nil
}

func (o *PostgresObserver) Detection(ctx context.Context, id int64) (Detection, error) {
	return scanDetection(o.pool.QueryRow(ctx, `SELECT `+detectionCols+` FROM detections WHERE id = $1`, id))
}

func (o *PostgresObserver) DetectionBySignature(ctx context.Context, sig string) (Detection, error) {
	return scanDetection(o.pool.QueryRow(ctx, `SELECT `+detectionCols+` FROM detections WHERE signature = $1`, sig))
}

func (o *PostgresObserver) Detections(ctx context.Context, limit int, statuses ...string) ([]Detection, error) {
	if limit <= 0 {
		limit = 200
	}
	if statuses == nil {
		statuses = []string{} // nil would encode as NULL
	}
	rows, err := o.pool.Query(ctx, `
		SELECT `+detectionCols+` FROM detections
		WHERE cardinality($1::text[]) = 0 OR status = ANY($1)
		ORDER BY last_seen DESC, id DESC LIMIT $2`, statuses, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Detection, error) { return scanDetection(r) })
}

func (o *PostgresObserver) SetDetection(ctx context.Context, id int64, status string, a Action) error {
	action, err := json.Marshal(a)
	if err != nil {
		return err
	}
	tag, err := o.pool.Exec(ctx, `UPDATE detections SET status = $2, action = $3 WHERE id = $1`, id, status, action)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (o *PostgresObserver) MarkDetectionsSeen(ctx context.Context, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := o.pool.Exec(ctx, `UPDATE detections SET seen_at = now() WHERE id = ANY($1) AND seen_at IS NULL`, ids)
	return err
}

func (o *PostgresObserver) UnseenDetections(ctx context.Context) (int, error) {
	var n int
	err := o.pool.QueryRow(ctx, `SELECT count(*) FROM detections WHERE seen_at IS NULL`).Scan(&n)
	return n, err
}

func (o *PostgresObserver) DetectionCounts(ctx context.Context) (map[string]int, error) {
	rows, err := o.pool.Query(ctx, `SELECT status, count(*) FROM detections GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

const generatedCols = `collection, item, field, value, expect, model, source_hash, status, created_at, updated_at`

func scanGenerated(r pgx.Row) (GeneratedField, error) {
	var g GeneratedField
	err := r.Scan(&g.Collection, &g.Item, &g.Field, &g.Value, &g.Expect, &g.Model, &g.SourceHash, &g.Status, &g.CreatedAt, &g.UpdatedAt)
	return g, notFound(err)
}

func (o *PostgresObserver) GeneratedField(ctx context.Context, collection, item, field string) (GeneratedField, error) {
	return scanGenerated(o.pool.QueryRow(ctx, `SELECT `+generatedCols+` FROM generated_fields
		WHERE collection = $1 AND item = $2 AND field = $3`, collection, item, field))
}

func (o *PostgresObserver) PutGeneratedField(ctx context.Context, g GeneratedField) error {
	if g.Status == "" {
		g.Status = GenActive
	}
	if g.Expect == "" {
		g.Expect = "text"
	}
	_, err := o.pool.Exec(ctx, `
		INSERT INTO generated_fields (collection, item, field, value, expect, model, source_hash, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (collection, item, field) DO UPDATE
		SET value = excluded.value, expect = excluded.expect, model = excluded.model,
		    source_hash = excluded.source_hash, status = excluded.status, updated_at = now()`,
		g.Collection, g.Item, g.Field, g.Value, g.Expect, g.Model, g.SourceHash, g.Status)
	return err
}

func (o *PostgresObserver) SetGeneratedStatus(ctx context.Context, collection, item, field, status string) error {
	tag, err := o.pool.Exec(ctx, `UPDATE generated_fields SET status = $4, updated_at = now()
		WHERE collection = $1 AND item = $2 AND field = $3`, collection, item, field, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (o *PostgresObserver) GeneratedFields(ctx context.Context, statuses ...string) ([]GeneratedField, error) {
	if statuses == nil {
		statuses = []string{} // nil would encode as NULL
	}
	rows, err := o.pool.Query(ctx, `SELECT `+generatedCols+` FROM generated_fields
		WHERE cardinality($1::text[]) = 0 OR status = ANY($1)
		ORDER BY collection, item, field`, statuses)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (GeneratedField, error) { return scanGenerated(r) })
}

// rebuildLock is a pg_advisory_xact_lock key serializing rebuild claims
// across instances.
const rebuildLock = 7_310_201_412

func (o *PostgresObserver) ClaimRebuild(ctx context.Context, r Rebuild, perDay int) (Rebuild, error) {
	var out Rebuild
	err := pgx.BeginFunc(ctx, o.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, rebuildLock); err != nil {
			return err
		}
		stale := RebuildStale.Seconds()
		var prevID int64
		var prevStatus string
		var prevStale bool
		err := tx.QueryRow(ctx, `SELECT id, status, started_at < now() - make_interval(secs => $3)
			FROM observer_rebuilds WHERE frontend = $1 AND fingerprint = $2`,
			r.Frontend, r.Fingerprint, stale).Scan(&prevID, &prevStatus, &prevStale)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			prevID = 0
		case err != nil:
			return err
		case prevStatus != RunRunning || !prevStale:
			return ErrRebuildDone
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM observer_rebuilds
			WHERE status = 'running' AND started_at >= now() - make_interval(secs => $1))`, stale).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrRebuildBusy
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM observer_rebuilds
			WHERE started_at >= now() - interval '24 hours'`).Scan(&n); err != nil {
			return err
		}
		if n >= perDay {
			return ErrRebuildQuota
		}
		var det *int64
		if r.DetectionID != 0 {
			det = &r.DetectionID
		}
		if prevID != 0 { // take over a dead attempt
			_, err = tx.Exec(ctx, `UPDATE observer_rebuilds SET detection_id = $2, parent = $3, revision = '',
				status = 'running', error = '', started_at = now(), finished_at = NULL WHERE id = $1`,
				prevID, det, r.Parent)
			r.ID = prevID
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO observer_rebuilds (frontend, fingerprint, detection_id, parent)
				VALUES ($1, $2, $3, $4) RETURNING id`, r.Frontend, r.Fingerprint, det, r.Parent).Scan(&r.ID)
		}
		if err != nil {
			return err
		}
		out, err = scanRebuild(tx.QueryRow(ctx, `SELECT `+rebuildCols+` FROM observer_rebuilds WHERE id = $1`, r.ID))
		return err
	})
	return out, err
}

const rebuildCols = `id, frontend, fingerprint, coalesce(detection_id, 0), parent, revision, status, error, started_at, finished_at`

func scanRebuild(r pgx.Row) (Rebuild, error) {
	var b Rebuild
	var fin *time.Time
	err := r.Scan(&b.ID, &b.Frontend, &b.Fingerprint, &b.DetectionID, &b.Parent, &b.Revision, &b.Status, &b.Error, &b.StartedAt, &fin)
	if fin != nil {
		b.FinishedAt = *fin
	}
	return b, notFound(err)
}

func (o *PostgresObserver) FinishRebuild(ctx context.Context, id int64, revision, errMsg string) error {
	status := RunDone
	if errMsg != "" {
		status = RunFailed
	}
	tag, err := o.pool.Exec(ctx, `UPDATE observer_rebuilds SET status = $2, revision = $3, error = $4, finished_at = now()
		WHERE id = $1`, id, status, revision, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (o *PostgresObserver) CancelRebuild(ctx context.Context, id int64) error {
	_, err := o.pool.Exec(ctx, `DELETE FROM observer_rebuilds WHERE id = $1`, id)
	return err
}

func (o *PostgresObserver) Rebuilds(ctx context.Context, limit int) ([]Rebuild, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := o.pool.Query(ctx, `SELECT `+rebuildCols+` FROM observer_rebuilds ORDER BY started_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rebuild, error) { return scanRebuild(r) })
}
