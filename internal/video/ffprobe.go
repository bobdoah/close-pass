package video

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Metadata struct {
	DurationSeconds float64
	RecordedAt      time.Time
	Camera          string // best-guess: nano, action5, unknown
}

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType string            `json:"codec_type"`
		Tags      map[string]string `json:"tags"`
	} `json:"streams"`
}

// Probe runs ffprobe against path and returns extracted metadata.
//
// DJI cameras embed creation_time in ISO-8601 form in either format.tags or
// the first video stream's tags. We try both. Camera identification looks at
// the encoder/handler/maker tags.
func Probe(ctx context.Context, ffprobePath, path string) (*Metadata, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var probed ffprobeOutput
	if err := json.Unmarshal(out, &probed); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	md := &Metadata{Camera: "unknown"}

	if probed.Format.Duration != "" {
		if d, err := strconv.ParseFloat(probed.Format.Duration, 64); err == nil {
			md.DurationSeconds = d
		}
	}

	if t := pickCreationTime(&probed); t != nil {
		md.RecordedAt = *t
	}

	md.Camera = pickCamera(&probed)

	return md, nil
}

func pickCreationTime(p *ffprobeOutput) *time.Time {
	candidates := []string{}
	if v, ok := p.Format.Tags["creation_time"]; ok {
		candidates = append(candidates, v)
	}
	for _, s := range p.Streams {
		if v, ok := s.Tags["creation_time"]; ok {
			candidates = append(candidates, v)
		}
	}
	for _, raw := range candidates {
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.000000Z",
		} {
			if t, err := time.Parse(layout, raw); err == nil {
				return &t
			}
		}
	}
	return nil
}

func pickCamera(p *ffprobeOutput) string {
	all := []map[string]string{p.Format.Tags}
	for _, s := range p.Streams {
		all = append(all, s.Tags)
	}
	for _, tags := range all {
		for _, key := range []string{"make", "model", "encoder", "handler_name", "comment"} {
			v := strings.ToLower(tags[key])
			switch {
			case strings.Contains(v, "nano"):
				return "nano"
			case strings.Contains(v, "action 5"), strings.Contains(v, "action5"):
				return "action5"
			case strings.Contains(v, "dji"):
				return "dji"
			}
		}
	}
	return "unknown"
}
