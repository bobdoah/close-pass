package watcher

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/bobdoah/close-pass/internal/db"
	"github.com/bobdoah/close-pass/internal/video"
)

// Watcher monitors the inbox directory for new video files and persists them
// to the store. It also performs an initial scan on startup to pick up files
// already present.
type Watcher struct {
	InboxDir    string
	FfprobePath string
	Store       *db.Store
	Log         *slog.Logger

	debounce sync.Map // path -> *time.Timer
}

var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".lrv": true,
}

func (w *Watcher) Run(ctx context.Context) error {
	if err := w.scanExisting(ctx); err != nil {
		w.Log.Warn("initial scan failed", "err", err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()
	if err := fw.Add(w.InboxDir); err != nil {
		return err
	}

	w.Log.Info("watching inbox", "dir", w.InboxDir)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-fw.Events:
			if !ok {
				return errors.New("watcher events channel closed")
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}
			if !isVideo(ev.Name) {
				continue
			}
			w.scheduleIngest(ctx, ev.Name)
		case err, ok := <-fw.Errors:
			if !ok {
				return errors.New("watcher errors channel closed")
			}
			w.Log.Warn("watcher error", "err", err)
		}
	}
}

func (w *Watcher) scanExisting(ctx context.Context) error {
	return filepath.WalkDir(w.InboxDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isVideo(path) {
			return nil
		}
		w.ingest(ctx, path)
		return nil
	})
}

// scheduleIngest debounces filesystem events so we don't try to read a file
// while it's still being copied. We wait for the file size to be stable for
// ~3 seconds before ingesting.
func (w *Watcher) scheduleIngest(ctx context.Context, path string) {
	const stabilizeDelay = 3 * time.Second
	if existing, ok := w.debounce.Load(path); ok {
		existing.(*time.Timer).Reset(stabilizeDelay)
		return
	}
	t := time.AfterFunc(stabilizeDelay, func() {
		w.debounce.Delete(path)
		if waitForStable(path, 5*time.Second) {
			w.ingest(ctx, path)
		}
	})
	w.debounce.Store(path, t)
}

func (w *Watcher) ingest(ctx context.Context, path string) {
	id, fresh, err := w.Store.InsertVideo(ctx, path)
	if err != nil {
		w.Log.Error("insert video", "path", path, "err", err)
		return
	}
	if !fresh {
		return
	}
	w.Log.Info("ingested", "path", path, "id", id)

	md, err := video.Probe(ctx, w.FfprobePath, path)
	if err != nil {
		w.Log.Warn("ffprobe failed", "path", path, "err", err)
		return
	}
	recordedAt := md.RecordedAt.Unix()
	if md.RecordedAt.IsZero() {
		// Fall back to the file's mtime so we have something to match on.
		if fi, ferr := os.Stat(path); ferr == nil {
			recordedAt = fi.ModTime().Unix()
		}
	}
	if err := w.Store.UpdateVideoMetadata(ctx, id, md.Camera, recordedAt, md.DurationSeconds); err != nil {
		w.Log.Error("update metadata", "id", id, "err", err)
	}
}

func waitForStable(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1
	for time.Now().Before(deadline) {
		fi, err := os.Stat(path)
		if err != nil {
			return false
		}
		if fi.Size() == lastSize && lastSize > 0 {
			return true
		}
		lastSize = fi.Size()
		time.Sleep(500 * time.Millisecond)
	}
	return lastSize > 0
}

func isVideo(path string) bool {
	return videoExts[strings.ToLower(filepath.Ext(path))]
}
