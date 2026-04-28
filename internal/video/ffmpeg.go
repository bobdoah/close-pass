package video

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type CutOptions struct {
	FfmpegPath string
	Source     string
	StartS     float64
	DurationS  float64
	Dest       string
}

// Cut extracts a sub-clip from Source. Uses stream copy where possible for
// speed (DJI files cut cleanly at GOP boundaries). If accurate cuts at
// arbitrary frames matter later, swap to re-encode mode.
func Cut(ctx context.Context, opts CutOptions) error {
	if opts.StartS < 0 {
		opts.StartS = 0
	}
	if err := os.MkdirAll(filepath.Dir(opts.Dest), 0o755); err != nil {
		return err
	}
	tmp := opts.Dest + ".part"
	args := []string{
		"-y",
		"-ss", strconv.FormatFloat(opts.StartS, 'f', 3, 64),
		"-i", opts.Source,
		"-t", strconv.FormatFloat(opts.DurationS, 'f', 3, 64),
		"-c", "copy",
		"-movflags", "+faststart",
		tmp,
	}
	cmd := exec.CommandContext(ctx, opts.FfmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg cut: %w (output: %s)", err, string(out))
	}
	return os.Rename(tmp, opts.Dest)
}

// Filename builds a clip filename of the form
// "2026-04-28T14-32-15_AB12CDE_<incidentid>.mp4". Registration may be empty.
func Filename(incidentID string, at time.Time, registration string) string {
	stamp := at.UTC().Format("2006-01-02T15-04-05")
	if registration == "" {
		return fmt.Sprintf("%s_%s.mp4", stamp, incidentID[:8])
	}
	return fmt.Sprintf("%s_%s_%s.mp4", stamp, registration, incidentID[:8])
}
