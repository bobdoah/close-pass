package strava

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Cache stores activities and streams in SQLite so the alignment UI doesn't
// hammer the Strava API on every page load.
type Cache struct{ DB *sql.DB }

func NewCache(db *sql.DB) *Cache { return &Cache{DB: db} }

func (c *Cache) UpsertActivities(ctx context.Context, acts []Activity) error {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO strava_activities (id, name, start_at, elapsed_s, distance_m, type, refreshed_at)
		VALUES (?, ?, ?, ?, ?, ?, unixepoch())
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			start_at = excluded.start_at,
			elapsed_s = excluded.elapsed_s,
			distance_m = excluded.distance_m,
			type = excluded.type,
			refreshed_at = excluded.refreshed_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range acts {
		if _, err := stmt.ExecContext(ctx,
			a.ID, a.Name, a.StartDate.Unix(), a.ElapsedSec, a.DistanceM, a.Type); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ActivitiesAround returns cached activities whose [start, start+elapsed]
// window overlaps [from, to].
func (c *Cache) ActivitiesAround(ctx context.Context, from, to time.Time) ([]Activity, error) {
	rows, err := c.DB.QueryContext(ctx, `
		SELECT id, name, start_at, elapsed_s, distance_m, type
		FROM strava_activities
		WHERE start_at + elapsed_s >= ? AND start_at <= ?
		ORDER BY start_at ASC`,
		from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		var startAt int64
		if err := rows.Scan(&a.ID, &a.Name, &startAt, &a.ElapsedSec, &a.DistanceM, &a.Type); err != nil {
			return nil, err
		}
		a.StartDate = time.Unix(startAt, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (c *Cache) UpsertStreams(ctx context.Context, activityID int64, s *Streams) error {
	tj, err := json.Marshal(s.Time)
	if err != nil {
		return err
	}
	llj, err := json.Marshal(s.LatLng)
	if err != nil {
		return err
	}
	_, err = c.DB.ExecContext(ctx, `
		INSERT INTO strava_streams (activity_id, times_json, latlng_json, refreshed_at)
		VALUES (?, ?, ?, unixepoch())
		ON CONFLICT(activity_id) DO UPDATE SET
			times_json = excluded.times_json,
			latlng_json = excluded.latlng_json,
			refreshed_at = excluded.refreshed_at`,
		activityID, string(tj), string(llj))
	return err
}

func (c *Cache) GetStreams(ctx context.Context, activityID int64) (*Streams, error) {
	row := c.DB.QueryRowContext(ctx,
		`SELECT times_json, latlng_json FROM strava_streams WHERE activity_id = ?`,
		activityID)
	var tj, llj string
	if err := row.Scan(&tj, &llj); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s := &Streams{}
	if err := json.Unmarshal([]byte(tj), &s.Time); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(llj), &s.LatLng); err != nil {
		return nil, err
	}
	return s, nil
}
