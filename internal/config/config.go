package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config is loaded from environment variables. See README for required vars.
type Config struct {
	// Storage
	DataDir string // location of sqlite db + oauth tokens
	DBPath  string // computed: DataDir/close-pass.db

	// Filesystem layout (paths inside the police-reports mount)
	ReportsRoot string // root mount path (e.g. /reports)
	InboxDir    string // To Process
	ToReportDir string // To Report
	ReportedDir string // Reported (manual)
	ClipsDir    string // Clips (created if missing)

	// HTTP
	HTTPAddr   string
	PublicURL  string // base URL for OAuth redirects (e.g. http://nas.lan:8080)
	BasicAuth  string // optional "user:pass" for basic auth on the UI

	// Strava
	StravaClientID     string
	StravaClientSecret string

	// YouTube / Google
	YouTubeClientID     string
	YouTubeClientSecret string

	// Video processing
	PreRollSeconds  int
	PostRollSeconds int
	FfmpegPath      string
	FfprobePath     string
}

func Load() (*Config, error) {
	c := &Config{
		DataDir:             getenv("DATA_DIR", "/data"),
		ReportsRoot:         getenv("REPORTS_ROOT", "/reports"),
		HTTPAddr:            getenv("HTTP_ADDR", ":8080"),
		PublicURL:           os.Getenv("PUBLIC_URL"),
		BasicAuth:           os.Getenv("BASIC_AUTH"),
		StravaClientID:      os.Getenv("STRAVA_CLIENT_ID"),
		StravaClientSecret:  os.Getenv("STRAVA_CLIENT_SECRET"),
		YouTubeClientID:     os.Getenv("YOUTUBE_CLIENT_ID"),
		YouTubeClientSecret: os.Getenv("YOUTUBE_CLIENT_SECRET"),
		PreRollSeconds:      getenvInt("PREROLL_SECONDS", 60),
		PostRollSeconds:     getenvInt("POSTROLL_SECONDS", 60),
		FfmpegPath:          getenv("FFMPEG_PATH", "ffmpeg"),
		FfprobePath:         getenv("FFPROBE_PATH", "ffprobe"),
	}

	c.DBPath = filepath.Join(c.DataDir, "close-pass.db")
	c.InboxDir = filepath.Join(c.ReportsRoot, "To Process")
	c.ToReportDir = filepath.Join(c.ReportsRoot, "To Report")
	c.ReportedDir = filepath.Join(c.ReportsRoot, "Reported (manual)")
	c.ClipsDir = filepath.Join(c.ReportsRoot, "Clips")

	if c.PublicURL == "" {
		return nil, errors.New("PUBLIC_URL is required (e.g. http://nas.lan:8080)")
	}
	if c.StravaClientID == "" || c.StravaClientSecret == "" {
		return nil, errors.New("STRAVA_CLIENT_ID and STRAVA_CLIENT_SECRET are required")
	}

	for _, dir := range []string{c.DataDir, c.InboxDir, c.ToReportDir, c.ReportedDir, c.ClipsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	return c, nil
}

// YouTubeEnabled reports whether YouTube uploads are configured.
func (c *Config) YouTubeEnabled() bool {
	return c.YouTubeClientID != "" && c.YouTubeClientSecret != ""
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
