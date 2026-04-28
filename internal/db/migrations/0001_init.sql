CREATE TABLE IF NOT EXISTS videos (
    id TEXT PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    file_hash TEXT,
    camera TEXT,                    -- "nano" | "action5" | "unknown"
    recorded_at INTEGER,            -- unix seconds, from ffprobe creation_time
    duration_s REAL,
    strava_activity_id INTEGER,     -- nullable until aligned
    gps_offset_s INTEGER NOT NULL DEFAULT 0,
                                    -- seconds to add to camera time to match Strava clock
    status TEXT NOT NULL,           -- DETECTED | AWAITING_ALIGNMENT | AWAITING_INCIDENTS
                                    -- | INCIDENTS_MARKED | DONE | ARCHIVED
    notes TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status);
CREATE INDEX IF NOT EXISTS idx_videos_recorded_at ON videos(recorded_at);

CREATE TABLE IF NOT EXISTS incidents (
    id TEXT PRIMARY KEY,
    video_id TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,

    -- Position within video
    t_seconds REAL NOT NULL,        -- timestamp inside the source video
    pre_roll_s INTEGER NOT NULL,
    post_roll_s INTEGER NOT NULL,

    -- Position in the world (looked up from Strava stream after alignment)
    incident_at INTEGER,            -- unix seconds
    lat REAL,
    lon REAL,

    -- User-entered evidence fields (mirrors the Google Sheet)
    incident_type TEXT,             -- Close pass | Failed to give way | ...
    make TEXT,
    model TEXT,
    registration TEXT,

    -- Generated artefacts
    clip_path TEXT,
    youtube_url TEXT,
    location_link TEXT,             -- OSM / Maps URL

    -- Police workflow
    report_number TEXT,
    incident_number TEXT,
    nip_sent INTEGER NOT NULL DEFAULT 0,  -- 0/1
    result TEXT,

    -- Pipeline status
    clip_status TEXT NOT NULL DEFAULT 'PENDING',
                                    -- PENDING | CUTTING | CUT | UPLOADING | UPLOADED | FAILED
    upload_error TEXT,

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_incidents_video ON incidents(video_id);
CREATE INDEX IF NOT EXISTS idx_incidents_clip_status ON incidents(clip_status);

-- Cached Strava activities for the alignment UI.
CREATE TABLE IF NOT EXISTS strava_activities (
    id INTEGER PRIMARY KEY,
    name TEXT,
    start_at INTEGER NOT NULL,      -- unix seconds, in UTC
    elapsed_s INTEGER NOT NULL,
    distance_m REAL,
    type TEXT,
    refreshed_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_strava_start ON strava_activities(start_at);

-- GPS streams cached per activity. Stored as a JSON blob to avoid a
-- one-row-per-sample blow-up; activities are typically <10k samples.
CREATE TABLE IF NOT EXISTS strava_streams (
    activity_id INTEGER PRIMARY KEY REFERENCES strava_activities(id) ON DELETE CASCADE,
    times_json TEXT NOT NULL,       -- []int (offset seconds from start)
    latlng_json TEXT NOT NULL,      -- [][2]float64
    refreshed_at INTEGER NOT NULL
);

-- OAuth tokens for external services (Strava, YouTube). Keyed by service name.
CREATE TABLE IF NOT EXISTS oauth_tokens (
    service TEXT PRIMARY KEY,       -- "strava" | "youtube"
    access_token TEXT NOT NULL,
    refresh_token TEXT,
    expiry INTEGER,                 -- unix seconds
    scope TEXT,
    updated_at INTEGER NOT NULL
);
