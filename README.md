# close-pass

Tooling for processing close-pass and similar incident videos for submission to
the Thames Valley Police road traffic incident report form.

Drop video files onto a NAS share, align each one to a Strava ride, mark the
moments of an incident in a web UI, and the tool extracts a clip, generates a
location link, optionally uploads to YouTube as unlisted, and records the
metadata to a local database that exports as CSV (matching the layout of the
existing Google Sheet).

## Status

This is the initial scaffolding. The currently working pieces:

- [x] Inbox watcher (fsnotify on `To Process/`).
- [x] ffprobe metadata extraction (creation time, duration, camera guess).
- [x] SQLite store with embedded migrations.
- [x] Strava OAuth + activity caching.
- [x] Web UI: video list, alignment screen, basic incident marking, incident
      list, CSV export.
- [ ] ffmpeg clip extraction (planned next).
- [ ] YouTube upload (planned next).
- [ ] OpenStreetMap link generation in the incident view (location link is
      written to DB but not rendered yet for new incidents).

## Architecture

```
police-reports/
├── To Process/         <- watcher input
├── To Report/          <- video moves here once incidents are extracted
├── Reported (manual)/  <- video moves here after report number entered
└── Clips/              <- generated incident clips (uploaded to YouTube)
```

The Go service:

1. Watches `To Process/` and ingests new files into SQLite.
2. ffprobe extracts creation time. The user pairs each video with a Strava
   activity in the UI; the camera-clock-to-Strava-clock offset is stored once
   per video.
3. The user scrubs the embedded video player, clicks "mark incident" at each
   close pass. Each incident's GPS coordinates are looked up in the cached
   Strava stream (camera time + offset → activity offset → lat/lon).
4. (Planned) A worker cuts ±60 s around each incident with ffmpeg and uploads
   to YouTube as unlisted.
5. The user fills in plate / make / model / report number / NIP status in the
   incident form. Everything exports as CSV.

## Setup

### Strava API app

You said you have one already. The redirect URL for the OAuth flow is:

```
${PUBLIC_URL}/auth/strava/callback
```

Set `Authorization Callback Domain` on the Strava app to match the host part
of `PUBLIC_URL` (e.g. `nas.lan`).

### Google Cloud project for YouTube upload (planned)

You don't have one yet. When the upload feature is added you'll need:

1. Create a project at <https://console.cloud.google.com>.
2. Enable the **YouTube Data API v3**.
3. Configure the OAuth consent screen as External, in Testing mode, with your
   own Google account added as a test user.
4. Create an OAuth 2.0 Client ID of type "Web application" with redirect URI
   `${PUBLIC_URL}/auth/youtube/callback`.
5. Set `YOUTUBE_CLIENT_ID` and `YOUTUBE_CLIENT_SECRET` env vars.

A single OAuth grant covers ~6 video uploads per day under the default
10,000-point quota; if you submit more, request a quota increase.

### Environment variables

Required:

- `PUBLIC_URL` — externally-reachable base URL for OAuth redirects, e.g.
  `http://nas.lan:8080`.
- `STRAVA_CLIENT_ID`, `STRAVA_CLIENT_SECRET`.

Optional:

- `YOUTUBE_CLIENT_ID`, `YOUTUBE_CLIENT_SECRET` — enables the (planned) upload
  feature.
- `BASIC_AUTH` — `user:pass` to gate the web UI. Omit for trusted-LAN.
- `DATA_DIR` (default `/data`) — SQLite location.
- `REPORTS_ROOT` (default `/reports`) — root of the police-reports mount.
- `HTTP_ADDR` (default `:8080`).
- `PREROLL_SECONDS` / `POSTROLL_SECONDS` (default 60 each).
- `FFMPEG_PATH`, `FFPROBE_PATH` — override binary lookup.
- `TZ` (default `Europe/London`).

### Running with Docker

```sh
cp .env.example .env       # then fill in
docker compose up --build
```

Mount `/mnt/police-reports` (or wherever the share lives on the host) into the
container at `/reports`. The directories `To Process/`, `To Report/`,
`Reported (manual)/`, and `Clips/` will be created automatically.

### Running on Kubernetes (home cluster)

The image is a single static binary; deploy as a Deployment with a PVC for
`/data` and an NFS mount for `/reports`. Expose via your usual Ingress.

## Development

```sh
go run ./cmd/close-pass
```

Requires `ffmpeg`/`ffprobe` on PATH. The SQLite database is created on first
run.

## Repo

<https://github.com/bobdoah/close-pass>
