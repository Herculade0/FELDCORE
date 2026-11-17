package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/docker"
	feldnames "github.com/Herculade0/feldcore/internal/names"
	"github.com/Herculade0/feldcore/internal/publish"
	"github.com/Herculade0/feldcore/internal/store"
)

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if s.publish == nil {
		writeError(w, http.StatusNotFound, "Publishing is not available.")
		return
	}
	writeJSON(w, http.StatusOK, s.publish.Overview(r.Context()))
}

func (s *Server) handlePublishSave(w http.ResponseWriter, r *http.Request) {
	if s.publish == nil {
		writeError(w, http.StatusNotFound, "Publishing is not available.")
		return
	}
	var body struct {
		BaseDomain    string   `json:"baseDomain"`
		RatePerSec    float64  `json:"ratePerSec"`
		RateBurst     int      `json:"rateBurst"`
		CountryMode   string   `json:"countryMode"`
		Countries     []string `json:"countries"`
		GeoEnabled    bool     `json:"geoEnabled"`
		LicenseKey    string   `json:"licenseKey"`
		ClearLicense  bool     `json:"clearLicense"`
		BlockBots     bool     `json:"blockBots"`
		BlockScanners bool     `json:"blockScanners"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	view, err := s.publish.SaveSettings(r.Context(), publish.SettingsInput{
		BaseDomain: body.BaseDomain, RatePerSec: body.RatePerSec, RateBurst: body.RateBurst,
		CountryMode: body.CountryMode, Countries: body.Countries, GeoEnabled: body.GeoEnabled,
		LicenseKey: body.LicenseKey, ClearLicense: body.ClearLicense,
		BlockBots: body.BlockBots, BlockScanners: body.BlockScanners,
	})
	body.LicenseKey = ""
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleContainerPublish(w http.ResponseWriter, r *http.Request) {
	info, ports, ok := s.publishTarget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.publish.Form(r.Context(), info.Spec.Name, info.Spec.WebUI, ports))
}

func (s *Server) handleContainerPublishSave(w http.ResponseWriter, r *http.Request) {
	info, ports, ok := s.publishTarget(w, r)
	if !ok {
		return
	}
	if info.Spec.Name == "" {
		writeError(w, http.StatusBadRequest, "This container has no name, so it cannot be published.")
		return
	}
	var body struct {
		Enabled   bool              `json:"enabled"`
		Subdomain string            `json:"subdomain"`
		Port      int               `json:"port"`
		Access    string            `json:"access"`
		Groups    []string          `json:"groups"`
		TLS       string            `json:"tls"`
		Brand     store.SignInBrand `json:"brand"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	out, err := s.publish.Set(r.Context(), publish.SetInput{
		Container: info.Spec.Name, Subdomain: body.Subdomain, Port: body.Port,
		Access: body.Access, Groups: body.Groups, TLS: body.TLS, Enabled: body.Enabled, Brand: body.Brand,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"warning": out.Warning,
		"publish": s.publish.Form(r.Context(), info.Spec.Name, info.Spec.WebUI, ports),
	})
}

func (s *Server) publishTarget(w http.ResponseWriter, r *http.Request) (docker.SpecInfo, []int, bool) {
	if s.publish == nil {
		writeError(w, http.StatusNotFound, "Publishing is not available.")
		return docker.SpecInfo{}, nil, false
	}
	if s.docker == nil {
		writeError(w, http.StatusNotFound, "Docker is not available.")
		return docker.SpecInfo{}, nil, false
	}
	info, err := s.docker.Spec(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return docker.SpecInfo{}, nil, false
	}
	return info, containerPorts(info), true
}

func containerPorts(info docker.SpecInfo) []int {
	var ports []int
	seen := map[int]bool{}
	add := func(p int) {
		if p > 0 && p <= 65535 && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	_, port, _ := feldnames.Suggest(info.Spec.Name, info.Spec.WebUI, "home.arpa")
	add(port)
	for _, c := range info.Spec.Configs {
		if c.Type == docker.TypePort {
			add(c.ContainerPort)
		}
	}
	if ports == nil {
		ports = []int{}
	}
	return ports
}
