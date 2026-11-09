package geo_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestParsePointAccepts(t *testing.T) {
	req.Covers(t, "NFR-SEC-002")
	for in, want := range map[string]geo.Point{
		"52.52,13.405":   {Lat: 52.52, Lon: 13.405},
		"-90,-180":       {Lat: -90, Lon: -180},
		"90,180":         {Lat: 90, Lon: 180},
		"0,0":            {},
		"0.000001,7":     {Lat: 0.000001, Lon: 7},
		"-0.5,-179.9999": {Lat: -0.5, Lon: -179.9999},
	} {
		got, err := geo.ParsePoint(in)
		if err != nil || got != want {
			t.Errorf("ParsePoint(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestParsePointRefuses(t *testing.T) {
	req.Covers(t, "NFR-SEC-002")
	for in, part := range map[string]string{
		"":             "lat,lon",
		"52.52":        "lat,lon",
		"52.52;13.4":   "lat,lon",
		"52.52,13.4,1": "longitude",
		"90.0001,0":    "latitude is outside [-90, 90]",
		"-90.5,0":      "latitude is outside",
		"0,180.01":     "longitude is outside [-180, 180]",
		"0,-181":       "longitude is outside",
		" 1,2":         "latitude is not a decimal number",
		"1, 2":         "longitude is not a decimal number",
		"NaN,1":        "latitude is not",
		"1,Inf":        "longitude is not",
		"1e1,2":        "latitude is not",
		"+1,2":         "latitude is not",
		"1.,2":         "latitude is not",
		".5,2":         "latitude is not",
		"-,2":          "latitude is not",
		"--1,2":        "latitude is not",
		"0x1,2":        "latitude is not",
		"1_0,2":        "latitude is not",
		"1,2.":         "longitude is not",
		"1,2.3.4":      "longitude is not",
		"١,2":          "latitude is not",
		"1,":           "longitude is not",
		",1":           "latitude is not",
		"1.5a,2":       "latitude is not",
	} {
		_, err := geo.ParsePoint(in)
		if !errors.Is(err, geo.ErrPoint) || !strings.Contains(err.Error(), part) {
			t.Errorf("ParsePoint(%q) = %v; want ErrPoint naming %q", in, err, part)
		}
	}
}

func TestRounded(t *testing.T) {
	req.Covers(t, "ENV-011")
	p := geo.Point{Lat: 52.12345649, Lon: -13.0000005}.Rounded()
	if p != (geo.Point{Lat: 52.123456, Lon: -13.000001}) {
		t.Errorf("Rounded = %v", p)
	}
	if got := geo.Round(1.23456, 2); got != 1.23 {
		t.Errorf("Round(1.23456, 2) = %v", got)
	}
	if got := geo.Round(2.5, 0); got != 3 {
		t.Errorf("Round(2.5, 0) = %v, want half away from zero", got)
	}
	if geo.Decimals != 6 {
		t.Errorf("Decimals = %d", geo.Decimals)
	}
}
