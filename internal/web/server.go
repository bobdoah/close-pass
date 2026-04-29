package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bobdoah/close-pass/internal/config"
	"github.com/bobdoah/close-pass/internal/db"
	"github.com/bobdoah/close-pass/internal/strava"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	Cfg          *config.Config
	Store        *db.Store
	StravaClient *strava.Client
	StravaCache  *strava.Cache
	Log          *slog.Logger

	tmpl *template.Template
}

// IncidentTypes mirrors the dropdown options in the original sheet.
var IncidentTypes = []string{
	"Close pass",
	"Failed to give way",
	"Dangerous overtake",
	"Mobile phone use",
	"Red light",
	"Other",
}

func New(cfg *config.Config, store *db.Store, sc *strava.Client, cache *strava.Cache, log *slog.Logger) (*Server, error) {
	t, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{
		Cfg: cfg, Store: store, StravaClient: sc, StravaCache: cache,
		Log: log, tmpl: t,
	}, nil
}

func loadTemplates() (*template.Template, error) {
	return template.ParseFS(templatesFS, "templates/*.gohtml")
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(s.basicAuth)

	staticSub, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	r.Get("/", s.handleIndex)
	r.Get("/videos", s.handleVideos)
	r.Get("/videos/{id}", s.handleVideo)
	r.Get("/videos/{id}/file", s.handleVideoFile)
	r.Get("/videos/{id}/align", s.handleAlign)
	r.Post("/videos/{id}/align", s.handleAlignSave)
	r.Post("/videos/{id}/incidents", s.handleCreateIncident)

	r.Get("/incidents", s.handleIncidents)
	r.Get("/incidents/{id}", s.handleIncident)
	r.Post("/incidents/{id}", s.handleIncidentSave)
	r.Post("/incidents/{id}/recut", s.handleIncidentRecut)

	r.Get("/export.csv", s.handleExportCSV)

	r.Get("/auth/strava", s.handleStravaStart)
	r.Get("/auth/strava/callback", s.handleStravaCallback)

	return r
}

// basicAuth wraps handlers in optional basic auth. Disabled if BASIC_AUTH is unset.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	creds := s.Cfg.BasicAuth
	if creds == "" {
		return next
	}
	parts := strings.SplitN(creds, ":", 2)
	if len(parts) != 2 {
		return next
	}
	wantUser, wantPass := parts[0], parts[1]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(wantUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(wantPass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="close-pass"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type pageData struct {
	Title           string
	StravaConnected bool
	InboxDir        string
	Videos          []VideoVM
	Video           VideoVM
	Incidents       []IncidentVM
	Incident        IncidentVM
	Candidates      []CandidateVM
	PreRollS        int
	PostRollS       int
	Types           []string
}

type CandidateVM struct {
	ID          int64
	Name        string
	StartFmt    string
	DurationFmt string
	DistanceKm  float64
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data pageData) {
	data.PreRollS = s.Cfg.PreRollSeconds
	data.PostRollS = s.Cfg.PostRollSeconds
	data.InboxDir = s.Cfg.InboxDir
	data.Types = IncidentTypes
	data.StravaConnected = s.stravaConnected(r.Context())

	t, err := s.tmpl.Clone()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := t.ParseFS(templatesFS, "templates/"+page+".gohtml"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.Log.Error("template render", "page", page, "err", err)
	}
}

func (s *Server) stravaConnected(ctx context.Context) bool {
	store := &strava.SQLTokenStore{DB: s.Store.DB, Service: "strava"}
	tok, err := store.Load(ctx)
	return err == nil && tok != nil && tok.AccessToken != ""
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/videos", http.StatusFound)
}

func (s *Server) handleVideos(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Store.ListVideos(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "videos", pageData{Title: "Videos", Videos: toVideoVMs(vs)})
}

func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Store.GetVideo(r.Context(), id)
	if err != nil {
		http.Error(w, "video not found", 404)
		return
	}
	ins, err := s.Store.ListIncidentsForVideo(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "video", pageData{
		Title: filepath.Base(v.Path), Video: toVideoVM(v), Incidents: toIncidentVMs(ins),
	})
}

func (s *Server) handleVideoFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Store.GetVideo(r.Context(), id)
	if err != nil {
		http.Error(w, "video not found", 404)
		return
	}
	f, err := os.Open(v.Path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (s *Server) handleAlign(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Store.GetVideo(r.Context(), id)
	if err != nil {
		http.Error(w, "video not found", 404)
		return
	}
	if !s.stravaConnected(r.Context()) {
		http.Redirect(w, r, "/auth/strava", http.StatusFound)
		return
	}

	var center time.Time
	if v.RecordedAt.Valid {
		center = time.Unix(v.RecordedAt.Int64, 0)
	} else {
		center = time.Now()
	}
	from := center.Add(-24 * time.Hour)
	to := center.Add(24 * time.Hour)

	if r.URL.Query().Get("refresh") == "1" {
		acts, err := s.StravaClient.ListActivitiesBetween(r.Context(), from, to)
		if err != nil {
			s.Log.Warn("strava list", "err", err)
		} else if err := s.StravaCache.UpsertActivities(r.Context(), acts); err != nil {
			s.Log.Warn("strava cache", "err", err)
		}
	} else {
		acts, err := s.StravaCache.ActivitiesAround(r.Context(), from, to)
		if err == nil && len(acts) == 0 {
			fresh, ferr := s.StravaClient.ListActivitiesBetween(r.Context(), from, to)
			if ferr == nil {
				_ = s.StravaCache.UpsertActivities(r.Context(), fresh)
			}
		}
	}

	acts, err := s.StravaCache.ActivitiesAround(r.Context(), from, to)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	cands := make([]CandidateVM, len(acts))
	for i, a := range acts {
		cands[i] = CandidateVM{
			ID:          a.ID,
			Name:        a.Name,
			StartFmt:    a.StartDate.Local().Format("2006-01-02 15:04"),
			DurationFmt: time.Duration(a.ElapsedSec * int(time.Second)).String(),
			DistanceKm:  a.DistanceM / 1000.0,
		}
	}

	s.render(w, r, "align", pageData{
		Title: "Align " + filepath.Base(v.Path), Video: toVideoVM(v), Candidates: cands,
	})
}

func (s *Server) handleAlignSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	activityID, err := strconv.ParseInt(r.FormValue("activity_id"), 10, 64)
	if err != nil {
		http.Error(w, "activity_id required", 400)
		return
	}
	offset, _ := strconv.ParseInt(r.FormValue("offset_s"), 10, 64)

	if err := s.Store.SetVideoAlignment(r.Context(), id, activityID, offset); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Any existing incidents on this video should be re-cut against the new
	// alignment.
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE incidents
		 SET clip_status = 'PENDING', upload_error = NULL,
		     incident_at = NULL, lat = NULL, lon = NULL, location_link = NULL,
		     updated_at = unixepoch()
		 WHERE video_id = ?`, id); err != nil {
		s.Log.Warn("requeue incidents after re-align", "video", id, "err", err)
	}

	// Pre-fetch the GPS stream so incident location lookups are fast.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		streams, err := s.StravaClient.FetchStreams(ctx, activityID)
		if err != nil {
			s.Log.Warn("strava streams", "activity", activityID, "err", err)
			return
		}
		if err := s.StravaCache.UpsertStreams(ctx, activityID, streams); err != nil {
			s.Log.Warn("strava streams cache", "activity", activityID, "err", err)
		}
	}()

	http.Redirect(w, r, "/videos/"+id, http.StatusFound)
}

func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, err := s.Store.GetVideo(r.Context(), id)
	if err != nil {
		http.Error(w, "video not found", 404)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	t, err := strconv.ParseFloat(r.FormValue("t_seconds"), 64)
	if err != nil {
		http.Error(w, "t_seconds required", 400)
		return
	}

	// The clip worker enriches with GPS data and cuts the clip asynchronously.
	in := &db.Incident{
		VideoID:   v.ID,
		TSeconds:  t,
		PreRollS:  s.Cfg.PreRollSeconds,
		PostRollS: s.Cfg.PostRollSeconds,
	}
	incidentID, err := s.Store.InsertIncident(r.Context(), in)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/incidents/"+incidentID, http.StatusFound)
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	ins, err := s.Store.ListAllIncidents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "incidents", pageData{Title: "Incidents", Incidents: toIncidentVMs(ins)})
}

func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	in, err := s.getIncident(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	v, err := s.Store.GetVideo(r.Context(), in.VideoID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "incident", pageData{
		Title: "Incident", Incident: toIncidentVM(in), Video: toVideoVM(v),
	})
}

func (s *Server) getIncident(ctx context.Context, id string) (*db.Incident, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, video_id, t_seconds, pre_roll_s, post_roll_s,
			incident_at, lat, lon, incident_type, make, model, registration,
			clip_path, youtube_url, location_link, report_number, incident_number,
			nip_sent, result, clip_status, upload_error, created_at, updated_at
		 FROM incidents WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*db.Incident
	for rows.Next() {
		var in db.Incident
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
	if len(out) == 0 {
		return nil, errors.New("not found")
	}
	return out[0], nil
}

func (s *Server) handleIncidentSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	nipSent := 0
	if r.FormValue("nip_sent") == "1" {
		nipSent = 1
	}
	_, err := s.Store.DB.ExecContext(r.Context(), `
		UPDATE incidents SET
			incident_type = NULLIF(?, ''),
			make = NULLIF(?, ''),
			model = NULLIF(?, ''),
			registration = NULLIF(?, ''),
			report_number = NULLIF(?, ''),
			incident_number = NULLIF(?, ''),
			nip_sent = ?,
			result = NULLIF(?, ''),
			updated_at = unixepoch()
		WHERE id = ?`,
		r.FormValue("incident_type"),
		r.FormValue("make"),
		r.FormValue("model"),
		strings.ToUpper(strings.ReplaceAll(r.FormValue("registration"), " ", "")),
		r.FormValue("report_number"),
		r.FormValue("incident_number"),
		nipSent,
		r.FormValue("result"),
		id,
	)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/incidents/"+id, http.StatusFound)
}

func (s *Server) handleIncidentRecut(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.Store.MarkPendingForRecut(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/incidents/"+id, http.StatusFound)
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ins, err := s.Store.ListAllIncidents(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="incidents.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cw.Write([]string{
		"Date", "Time", "Make", "Model", "Registration Number",
		"Location", "YouTube Link", "Report number", "Incident Number",
		"Incident", "NIP sent", "Result",
	})
	for _, in := range ins {
		vm := toIncidentVM(in)
		cw.Write([]string{
			vm.DateFmt(), vm.TimeFmt(),
			vm.MakeOr(""), vm.ModelOr(""), vm.RegistrationOr(""),
			vm.LocationLinkOr(""), vm.YouTubeURLOr(""),
			vm.ReportNumberOr(""), vm.IncidentNumberOr(""),
			vm.IncidentTypeOr(""),
			boolStr(vm.NIPSent), vm.ResultOr(""),
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func (s *Server) handleStravaStart(w http.ResponseWriter, r *http.Request) {
	state := randomState()
	http.SetCookie(w, &http.Cookie{
		Name: "strava_oauth_state", Value: state,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, s.StravaClient.AuthCodeURL(state), http.StatusFound)
}

func (s *Server) handleStravaCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("strava_oauth_state")
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "state mismatch", 400)
		return
	}
	if errStr := r.URL.Query().Get("error"); errStr != "" {
		http.Error(w, "strava error: "+errStr, 400)
		return
	}
	if err := s.StravaClient.ExchangeAndStore(r.Context(), r.URL.Query().Get("code")); err != nil {
		http.Error(w, "exchange: "+err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/videos", http.StatusFound)
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ListenAndServe runs the HTTP server until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: s.Cfg.HTTPAddr, Handler: s.Routes()}
	errCh := make(chan error, 1)
	go func() {
		s.Log.Info("listening", "addr", s.Cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http: %w", err)
	}
}
