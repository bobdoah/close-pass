package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type VideoStatus string

const (
	StatusDetected          VideoStatus = "DETECTED"
	StatusAwaitingAlignment VideoStatus = "AWAITING_ALIGNMENT"
	StatusAwaitingIncidents VideoStatus = "AWAITING_INCIDENTS"
	StatusIncidentsMarked   VideoStatus = "INCIDENTS_MARKED"
	StatusDone              VideoStatus = "DONE"
	StatusArchived          VideoStatus = "ARCHIVED"
)

type Video struct {
	ID               string
	Path             string
	FileHash         sql.NullString
	Camera           sql.NullString
	RecordedAt       sql.NullInt64
	DurationSeconds  sql.NullFloat64
	StravaActivityID sql.NullInt64
	GPSOffsetS       int64
	Status           VideoStatus
	Notes            sql.NullString
	CreatedAt        int64
	UpdatedAt        int64
}

type Incident struct {
	ID             string
	VideoID        string
	TSeconds       float64
	PreRollS       int
	PostRollS      int
	IncidentAt     sql.NullInt64
	Lat            sql.NullFloat64
	Lon            sql.NullFloat64
	IncidentType   sql.NullString
	Make           sql.NullString
	Model          sql.NullString
	Registration   sql.NullString
	ClipPath       sql.NullString
	YouTubeURL     sql.NullString
	LocationLink   sql.NullString
	ReportNumber   sql.NullString
	IncidentNumber sql.NullString
	NIPSent        bool
	Result         sql.NullString
	ClipStatus     string
	UploadError    sql.NullString
	CreatedAt      int64
	UpdatedAt      int64
}

type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// InsertVideo creates a new video row if the path is not already tracked.
// Returns the existing ID if it is.
func (s *Store) InsertVideo(ctx context.Context, path string) (string, bool, error) {
	var existing string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM videos WHERE path = ?`, path).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}

	id := uuid.NewString()
	now := time.Now().Unix()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO videos (id, path, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, path, StatusDetected, now, now)
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *Store) UpdateVideoMetadata(ctx context.Context, id string, camera string, recordedAt int64, duration float64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE videos
		 SET camera = ?, recorded_at = ?, duration_s = ?, updated_at = unixepoch(),
		     status = CASE WHEN status = ? THEN ? ELSE status END
		 WHERE id = ?`,
		camera, recordedAt, duration,
		StatusDetected, StatusAwaitingAlignment,
		id)
	return err
}

func (s *Store) SetVideoAlignment(ctx context.Context, id string, activityID int64, offset int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE videos
		 SET strava_activity_id = ?, gps_offset_s = ?, status = ?, updated_at = unixepoch()
		 WHERE id = ?`,
		activityID, offset, StatusAwaitingIncidents, id)
	return err
}

func (s *Store) SetVideoStatus(ctx context.Context, id string, status VideoStatus) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE videos SET status = ?, updated_at = unixepoch() WHERE id = ?`,
		status, id)
	return err
}

func (s *Store) GetVideo(ctx context.Context, id string) (*Video, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, path, file_hash, camera, recorded_at, duration_s,
		        strava_activity_id, gps_offset_s, status, notes, created_at, updated_at
		 FROM videos WHERE id = ?`, id)
	var v Video
	if err := row.Scan(&v.ID, &v.Path, &v.FileHash, &v.Camera, &v.RecordedAt,
		&v.DurationSeconds, &v.StravaActivityID, &v.GPSOffsetS, &v.Status,
		&v.Notes, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *Store) ListVideos(ctx context.Context) ([]*Video, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, path, file_hash, camera, recorded_at, duration_s,
		        strava_activity_id, gps_offset_s, status, notes, created_at, updated_at
		 FROM videos ORDER BY recorded_at DESC NULLS LAST, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Video
	for rows.Next() {
		var v Video
		if err := rows.Scan(&v.ID, &v.Path, &v.FileHash, &v.Camera, &v.RecordedAt,
			&v.DurationSeconds, &v.StravaActivityID, &v.GPSOffsetS, &v.Status,
			&v.Notes, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

func (s *Store) InsertIncident(ctx context.Context, in *Incident) (string, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	now := time.Now().Unix()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.ClipStatus == "" {
		in.ClipStatus = "PENDING"
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO incidents (
			id, video_id, t_seconds, pre_roll_s, post_roll_s,
			incident_at, lat, lon,
			clip_status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.VideoID, in.TSeconds, in.PreRollS, in.PostRollS,
		in.IncidentAt, in.Lat, in.Lon,
		in.ClipStatus, in.CreatedAt, in.UpdatedAt)
	return in.ID, err
}

func (s *Store) ListIncidentsForVideo(ctx context.Context, videoID string) ([]*Incident, error) {
	rows, err := s.DB.QueryContext(ctx, incidentSelectColumns+
		` FROM incidents WHERE video_id = ? ORDER BY t_seconds ASC`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIncidents(rows)
}

func (s *Store) ListAllIncidents(ctx context.Context) ([]*Incident, error) {
	rows, err := s.DB.QueryContext(ctx, incidentSelectColumns+
		` FROM incidents ORDER BY incident_at DESC NULLS LAST, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIncidents(rows)
}

const incidentSelectColumns = `SELECT id, video_id, t_seconds, pre_roll_s, post_roll_s,
	incident_at, lat, lon, incident_type, make, model, registration,
	clip_path, youtube_url, location_link, report_number, incident_number,
	nip_sent, result, clip_status, upload_error, created_at, updated_at`

func scanIncidents(rows *sql.Rows) ([]*Incident, error) {
	var out []*Incident
	for rows.Next() {
		var in Incident
		var nip int64
		if err := rows.Scan(
			&in.ID, &in.VideoID, &in.TSeconds, &in.PreRollS, &in.PostRollS,
			&in.IncidentAt, &in.Lat, &in.Lon, &in.IncidentType, &in.Make, &in.Model,
			&in.Registration, &in.ClipPath, &in.YouTubeURL, &in.LocationLink,
			&in.ReportNumber, &in.IncidentNumber, &nip, &in.Result,
			&in.ClipStatus, &in.UploadError, &in.CreatedAt, &in.UpdatedAt,
		); err != nil {
			return nil, err
		}
		in.NIPSent = nip != 0
		out = append(out, &in)
	}
	return out, rows.Err()
}
