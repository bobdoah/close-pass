package web

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bobdoah/close-pass/internal/db"
)

// VideoVM is the template-side view of a Video row.
type VideoVM struct {
	*db.Video
	Basename      string
	RecordedAtFmt string
	DurationFmt   string
}

// IncidentVM is the template-side view of an Incident row.
type IncidentVM struct {
	*db.Incident
	IncidentAtFmt string
}

func toVideoVM(v *db.Video) VideoVM {
	vm := VideoVM{Video: v, Basename: filepath.Base(v.Path)}
	if v.RecordedAt.Valid {
		vm.RecordedAtFmt = time.Unix(v.RecordedAt.Int64, 0).Local().Format("2006-01-02 15:04:05")
	} else {
		vm.RecordedAtFmt = "—"
	}
	if v.DurationSeconds.Valid {
		d := time.Duration(v.DurationSeconds.Float64 * float64(time.Second)).Round(time.Second)
		vm.DurationFmt = d.String()
	} else {
		vm.DurationFmt = "—"
	}
	return vm
}

func toVideoVMs(in []*db.Video) []VideoVM {
	out := make([]VideoVM, len(in))
	for i, v := range in {
		out[i] = toVideoVM(v)
	}
	return out
}

func toIncidentVM(in *db.Incident) IncidentVM {
	vm := IncidentVM{Incident: in}
	if in.IncidentAt.Valid {
		vm.IncidentAtFmt = time.Unix(in.IncidentAt.Int64, 0).Local().Format("2006-01-02 15:04:05")
	} else {
		vm.IncidentAtFmt = "—"
	}
	return vm
}

func toIncidentVMs(in []*db.Incident) []IncidentVM {
	out := make([]IncidentVM, len(in))
	for i, v := range in {
		out[i] = toIncidentVM(v)
	}
	return out
}

func (v IncidentVM) DateFmt() string {
	if !v.IncidentAt.Valid {
		return "—"
	}
	return time.Unix(v.IncidentAt.Int64, 0).Local().Format("2006-01-02")
}

func (v IncidentVM) TimeFmt() string {
	if !v.IncidentAt.Valid {
		return "—"
	}
	return time.Unix(v.IncidentAt.Int64, 0).Local().Format("15:04:05")
}

func (v IncidentVM) MakeOr(fb string) string           { return nsOr(v.Make, fb) }
func (v IncidentVM) ModelOr(fb string) string          { return nsOr(v.Model, fb) }
func (v IncidentVM) RegistrationOr(fb string) string   { return nsOr(v.Registration, fb) }
func (v IncidentVM) IncidentTypeOr(fb string) string   { return nsOr(v.IncidentType, fb) }
func (v IncidentVM) ResultOr(fb string) string         { return nsOr(v.Result, fb) }
func (v IncidentVM) ReportNumberOr(fb string) string   { return nsOr(v.ReportNumber, fb) }
func (v IncidentVM) IncidentNumberOr(fb string) string { return nsOr(v.IncidentNumber, fb) }
func (v IncidentVM) LocationLinkOr(fb string) string   { return nsOr(v.LocationLink, fb) }
func (v IncidentVM) YouTubeURLOr(fb string) string     { return nsOr(v.YouTubeURL, fb) }

func (v IncidentVM) MakeModelOr(fb string) string {
	mk, md := nsOr(v.Make, ""), nsOr(v.Model, "")
	switch {
	case mk == "" && md == "":
		return fb
	case mk == "":
		return md
	case md == "":
		return mk
	}
	return fmt.Sprintf("%s %s", mk, md)
}

func nsOr(s sql.NullString, fb string) string {
	if !s.Valid || s.String == "" {
		return fb
	}
	return s.String
}
