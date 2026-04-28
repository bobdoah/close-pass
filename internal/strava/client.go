package strava

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/oauth2"
)

const (
	authURL  = "https://www.strava.com/oauth/authorize"
	tokenURL = "https://www.strava.com/api/v3/oauth/token"
	apiBase  = "https://www.strava.com/api/v3"
	scopes   = "read,activity:read_all"
)

// Config returns an oauth2.Config bound to the given client credentials and
// redirect URL.
func Config(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
		RedirectURL: redirectURL,
		Scopes:      []string{scopes},
	}
}

type Activity struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	StartDate  time.Time `json:"start_date"`
	ElapsedSec int       `json:"elapsed_time"`
	DistanceM  float64   `json:"distance"`
}

type Streams struct {
	// each stream is keyed by type ("time", "latlng", ...).
	Time   []int        `json:"-"`
	LatLng [][2]float64 `json:"-"`
}

type Client struct {
	cfg   *oauth2.Config
	store TokenStore
	http  *http.Client
}

type TokenStore interface {
	Load(ctx context.Context) (*oauth2.Token, error)
	Save(ctx context.Context, tok *oauth2.Token) error
}

func New(cfg *oauth2.Config, store TokenStore) *Client {
	return &Client{cfg: cfg, store: store, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) AuthCodeURL(state string) string {
	// Strava uses approval_prompt=auto by default; force=auto on subsequent re-auths.
	return c.cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (c *Client) ExchangeAndStore(ctx context.Context, code string) error {
	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return err
	}
	return c.store.Save(ctx, tok)
}

// httpClient returns an oauth2-aware http.Client, refreshing the stored token
// if needed.
func (c *Client) httpClient(ctx context.Context) (*http.Client, error) {
	tok, err := c.store.Load(ctx)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, ErrNotAuthenticated
	}
	src := c.cfg.TokenSource(ctx, tok)
	refreshed, err := src.Token()
	if err != nil {
		return nil, err
	}
	if refreshed.AccessToken != tok.AccessToken {
		_ = c.store.Save(ctx, refreshed)
	}
	return oauth2.NewClient(ctx, src), nil
}

// ListActivitiesBetween returns activities whose start time is between
// after and before (inclusive lower, exclusive upper).
func (c *Client) ListActivitiesBetween(ctx context.Context, after, before time.Time) ([]Activity, error) {
	hc, err := c.httpClient(ctx)
	if err != nil {
		return nil, err
	}
	var out []Activity
	page := 1
	for {
		q := url.Values{}
		q.Set("after", strconv.FormatInt(after.Unix(), 10))
		q.Set("before", strconv.FormatInt(before.Unix(), 10))
		q.Set("per_page", "50")
		q.Set("page", strconv.Itoa(page))

		req, _ := http.NewRequestWithContext(ctx, "GET",
			apiBase+"/athlete/activities?"+q.Encode(), nil)
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("strava activities: status %d", resp.StatusCode)
		}
		var batch []Activity
		err = json.NewDecoder(resp.Body).Decode(&batch)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 50 {
			break
		}
		page++
	}
	return out, nil
}

// FetchStreams pulls time + latlng streams for an activity.
func (c *Client) FetchStreams(ctx context.Context, activityID int64) (*Streams, error) {
	hc, err := c.httpClient(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("keys", "time,latlng")
	q.Set("key_by_type", "true")

	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/activities/%d/streams?%s", apiBase, activityID, q.Encode()), nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("strava streams: status %d", resp.StatusCode)
	}

	var keyed map[string]struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keyed); err != nil {
		return nil, err
	}
	s := &Streams{}
	if t, ok := keyed["time"]; ok {
		_ = json.Unmarshal(t.Data, &s.Time)
	}
	if ll, ok := keyed["latlng"]; ok {
		_ = json.Unmarshal(ll.Data, &s.LatLng)
	}
	return s, nil
}

// LookupAt finds the lat/lon at offset seconds inside the activity. Returns
// (0,0,false) when out of range or no GPS at that point.
func (s *Streams) LookupAt(offset int) (lat, lon float64, ok bool) {
	if len(s.Time) == 0 || len(s.LatLng) == 0 {
		return 0, 0, false
	}
	// time stream is monotonic offset-from-start in seconds.
	idx := indexLE(s.Time, offset)
	if idx < 0 {
		return 0, 0, false
	}
	if idx >= len(s.LatLng) {
		idx = len(s.LatLng) - 1
	}
	ll := s.LatLng[idx]
	return ll[0], ll[1], true
}

func indexLE(times []int, target int) int {
	lo, hi := 0, len(times)-1
	if hi < 0 || times[0] > target {
		return -1
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if times[mid] <= target {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

var ErrNotAuthenticated = fmt.Errorf("strava not authenticated")

// SQLTokenStore persists tokens to the oauth_tokens table.
type SQLTokenStore struct {
	DB      *sql.DB
	Service string
}

func (s *SQLTokenStore) Load(ctx context.Context) (*oauth2.Token, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT access_token, refresh_token, expiry, scope FROM oauth_tokens WHERE service = ?`,
		s.Service)
	var tok oauth2.Token
	var expiry sql.NullInt64
	var scope sql.NullString
	if err := row.Scan(&tok.AccessToken, &tok.RefreshToken, &expiry, &scope); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if expiry.Valid {
		tok.Expiry = time.Unix(expiry.Int64, 0)
	}
	return &tok, nil
}

func (s *SQLTokenStore) Save(ctx context.Context, tok *oauth2.Token) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO oauth_tokens (service, access_token, refresh_token, expiry, scope, updated_at)
		VALUES (?, ?, ?, ?, ?, unixepoch())
		ON CONFLICT(service) DO UPDATE SET
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expiry = excluded.expiry,
			updated_at = excluded.updated_at`,
		s.Service, tok.AccessToken, tok.RefreshToken, tok.Expiry.Unix(), "")
	return err
}
