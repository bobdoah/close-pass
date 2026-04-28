package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bobdoah/close-pass/internal/config"
	"github.com/bobdoah/close-pass/internal/db"
	"github.com/bobdoah/close-pass/internal/strava"
	"github.com/bobdoah/close-pass/internal/watcher"
	"github.com/bobdoah/close-pass/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sqldb, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer sqldb.Close()
	store := db.NewStore(sqldb)

	stravaCfg := strava.Config(
		cfg.StravaClientID, cfg.StravaClientSecret,
		cfg.PublicURL+"/auth/strava/callback",
	)
	tokenStore := &strava.SQLTokenStore{DB: sqldb, Service: "strava"}
	stravaClient := strava.New(stravaCfg, tokenStore)
	stravaCache := strava.NewCache(sqldb)

	wch := &watcher.Watcher{
		InboxDir:    cfg.InboxDir,
		FfprobePath: cfg.FfprobePath,
		Store:       store,
		Log:         log.With("component", "watcher"),
	}
	go func() {
		if err := wch.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("watcher", "err", err)
		}
	}()

	srv, err := web.New(cfg, store, stravaClient, stravaCache, log.With("component", "web"))
	if err != nil {
		log.Error("server init", "err", err)
		os.Exit(1)
	}

	log.Info("close-pass starting",
		"db", cfg.DBPath,
		"inbox", cfg.InboxDir,
		"clips", cfg.ClipsDir,
		"public_url", cfg.PublicURL,
	)
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
}
