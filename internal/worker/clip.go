package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/bobdoah/close-pass/internal/config"
	"github.com/bobdoah/close-pass/internal/db"
	"github.com/bobdoah/close-pass/internal/geo"
	"github.com/bobdoah/close-pass/internal/strava"
	"github.com/bobdoah/close-pass/internal/video"
)

// Clip processes incidents in PENDING state: enriches with GPS data if needed,
// then cuts the source video using ffmpeg.
type Clip struct {
	Cfg         *config.Config
	Store       *db.Store
	StravaCache *strava.Cache
	Log         *slog.Logger
	Poll        time.Duration
}

func (c *Clip) Run(ctx context.Context) error {
	if c.Poll == 0 {
		c.Poll = 5 * time.Second
	}
	t := time.NewTicker(c.Poll)
	defer t.Stop()

	for {
		// Drain the queue between ticks so a fresh incident is picked up
		// without waiting a full poll interval.
		for c.processOne(ctx) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// processOne returns true if an incident was processed (regardless of
// success), so the caller knows to keep draining.
func (c *Clip) processOne(ctx context.Context) bool {
	id, err := c.Store.ClaimPendingIncident(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.Log.Warn("claim incident", "err", err)
		}
		return false
	}

	in, err := c.Store.GetIncident(ctx, id)
	if err != nil {
		c.Log.Error("load claimed incident", "id", id, "err", err)
		_ = c.Store.MarkIncidentFailed(ctx, id, "load incident: "+err.Error())
		return true
	}
	v, err := c.Store.GetVideo(ctx, in.VideoID)
	if err != nil {
		c.Log.Error("load incident video", "id", id, "err", err)
		_ = c.Store.MarkIncidentFailed(ctx, id, "load video: "+err.Error())
		return true
	}

	if err := c.enrich(ctx, v, in); err != nil {
		c.Log.Warn("enrich", "id", id, "err", err)
		// Non-fatal: we can still cut the clip even without GPS.
	}

	if err := c.cut(ctx, v, in); err != nil {
		c.Log.Error("cut", "id", id, "err", err)
		_ = c.Store.MarkIncidentFailed(ctx, id, err.Error())
		return true
	}
	return true
}

// enrich fills incident_at, lat, lon, location_link if they aren't already set
// and the enclosing video is aligned to a Strava activity.
func (c *Clip) enrich(ctx context.Context, v *db.Video, in *db.Incident) error {
	if in.IncidentAt.Valid && in.Lat.Valid && in.Lon.Valid && in.LocationLink.Valid {
		return nil
	}
	if !v.RecordedAt.Valid {
		return errors.New("video has no recorded_at; cannot derive incident time")
	}

	absTime := v.RecordedAt.Int64 + int64(in.TSeconds) + v.GPSOffsetS

	var lat, lon float64
	var link string
	if v.StravaActivityID.Valid {
		streams, err := c.StravaCache.GetStreams(ctx, v.StravaActivityID.Int64)
		if err == nil && streams != nil {
			start, err := c.activityStart(ctx, v.StravaActivityID.Int64)
			if err == nil && !start.IsZero() {
				offset := int(absTime - start.Unix())
				if la, lo, ok := streams.LookupAt(offset); ok {
					lat, lon = la, lo
					link = geo.OSMLink(la, lo)
				}
			}
		}
	}

	return c.Store.EnrichIncident(ctx, in.ID, absTime, lat, lon, link)
}

func (c *Clip) cut(ctx context.Context, v *db.Video, in *db.Incident) error {
	if !v.DurationSeconds.Valid {
		return errors.New("video has no probed duration")
	}

	dur := v.DurationSeconds.Float64
	pre := float64(in.PreRollS)
	post := float64(in.PostRollS)

	start := in.TSeconds - pre
	if start < 0 {
		start = 0
	}
	end := in.TSeconds + post
	if end > dur {
		end = dur
	}
	if end <= start {
		return fmt.Errorf("invalid cut window: start=%.2f end=%.2f", start, end)
	}

	at := time.Now().UTC()
	if in.IncidentAt.Valid {
		at = time.Unix(in.IncidentAt.Int64, 0).UTC()
	}
	reg := ""
	if in.Registration.Valid {
		reg = in.Registration.String
	}
	name := video.Filename(in.ID, at, reg)
	dest := filepath.Join(c.Cfg.ToReportDir, name)

	c.Log.Info("cutting clip",
		"incident", in.ID, "src", v.Path, "start", start, "dur", end-start, "dest", dest)

	if err := video.Cut(ctx, video.CutOptions{
		FfmpegPath: c.Cfg.FfmpegPath,
		Source:     v.Path,
		StartS:     start,
		DurationS:  end - start,
		Dest:       dest,
	}); err != nil {
		return fmt.Errorf("ffmpeg: %w", err)
	}
	return c.Store.MarkIncidentCut(ctx, in.ID, dest)
}

func (c *Clip) activityStart(ctx context.Context, activityID int64) (time.Time, error) {
	row := c.Store.DB.QueryRowContext(ctx,
		`SELECT start_at FROM strava_activities WHERE id = ?`, activityID)
	var startAt int64
	if err := row.Scan(&startAt); err != nil {
		return time.Time{}, err
	}
	return time.Unix(startAt, 0), nil
}
