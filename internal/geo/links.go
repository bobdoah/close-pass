package geo

import "fmt"

// OSMLink returns an OpenStreetMap permalink centred on lat/lon with a
// marker. Coordinates are clamped to 6 decimal places (~11 cm at the equator).
func OSMLink(lat, lon float64) string {
	return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.6f&mlon=%.6f#map=18/%.6f/%.6f",
		lat, lon, lat, lon)
}

// GoogleMapsLink returns a Google Maps URL with a marker pin.
func GoogleMapsLink(lat, lon float64) string {
	return fmt.Sprintf("https://www.google.com/maps?q=%.6f,%.6f", lat, lon)
}

// StreetViewLink returns a Google Street View URL pointing at the location.
// Heading defaults to north.
func StreetViewLink(lat, lon float64) string {
	return fmt.Sprintf("https://www.google.com/maps?layer=c&cbll=%.6f,%.6f", lat, lon)
}
