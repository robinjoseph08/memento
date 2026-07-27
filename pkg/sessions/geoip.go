package sessions

import (
	"fmt"
	"net"
	"strings"

	"github.com/oschwald/geoip2-golang"
)

// LocalGeoIP resolves approximate Session locations without a network lookup.
type LocalGeoIP struct{ database *geoip2.Reader }

// OpenLocalGeoIP opens an operator-provided MaxMind-compatible City database.
func OpenLocalGeoIP(path string) (*LocalGeoIP, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	database, err := geoip2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local GeoIP database: %w", err)
	}
	return &LocalGeoIP{database: database}, nil
}

// Lookup returns a coarse city/region/country label from local data only.
func (g *LocalGeoIP) Lookup(ip net.IP) string {
	if g == nil || g.database == nil || ip == nil {
		return ""
	}
	record, err := g.database.City(ip)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if city := record.City.Names["en"]; city != "" {
		parts = append(parts, city)
	}
	if len(record.Subdivisions) > 0 {
		if region := record.Subdivisions[0].Names["en"]; region != "" && region != first(parts) {
			parts = append(parts, region)
		}
	}
	if country := record.Country.Names["en"]; country != "" && country != first(parts) {
		parts = append(parts, country)
	}
	return strings.Join(parts, ", ")
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Close releases the local database mapping.
func (g *LocalGeoIP) Close() error {
	if g == nil || g.database == nil {
		return nil
	}
	return g.database.Close()
}
