package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// seriesCap is one sample a minute for a week. Tests shrink it.
var seriesCap = 7 * 24 * 60

// Point is one minute on the health graphs.
type Point struct {
	T    int64              `json:"t"`
	CPU  float64            `json:"cpu"`
	Mem  float64            `json:"mem"`
	Load float64            `json:"load"`
	Disk map[string]float64 `json:"disk,omitempty"`
	Temp map[string]float64 `json:"temp,omitempty"`
}

// Series is a ring of points stored as one JSON file.
type Series struct {
	mu     sync.Mutex
	path   string
	points []Point
}

// OpenSeries loads an existing ring or starts an empty one. A corrupt file
// is ignored so a bad write cannot stop the daemon.
func OpenSeries(path string) *Series {
	s := &Series{path: path}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var file struct {
		Points []Point `json:"points"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return s
	}
	if len(file.Points) > seriesCap {
		file.Points = file.Points[len(file.Points)-seriesCap:]
	}
	s.points = file.Points
	return s
}

// Add appends a point and drops the oldest past the cap.
func (s *Series) Add(p Point) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.points = append(s.points, p)
	if len(s.points) > seriesCap {
		s.points = append([]Point(nil), s.points[len(s.points)-seriesCap:]...)
	}
	points := append([]Point(nil), s.points...)
	path := s.path
	s.mu.Unlock()
	if path != "" {
		_ = writeSeries(path, points)
	}
}

// Since returns points at or after since, downsampled to at most max items.
func (s *Series) Since(since time.Time, max int) []Point {
	if s == nil {
		return nil
	}
	if max <= 0 {
		max = 360
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	start := 0
	if !since.IsZero() {
		cut := since.Unix()
		for start < len(s.points) && s.points[start].T < cut {
			start++
		}
	}
	in := s.points[start:]
	if len(in) == 0 {
		return []Point{}
	}
	stride := 1
	if len(in) > max {
		stride = (len(in) + max - 1) / max
	}
	out := make([]Point, 0, (len(in)+stride-1)/stride)
	for i := 0; i < len(in); i += stride {
		p := in[i]
		if p.Disk != nil {
			d := make(map[string]float64, len(p.Disk))
			for k, v := range p.Disk {
				d[k] = v
			}
			p.Disk = d
		}
		if p.Temp != nil {
			tm := make(map[string]float64, len(p.Temp))
			for k, v := range p.Temp {
				tm[k] = v
			}
			p.Temp = tm
		}
		out = append(out, p)
	}
	return out
}

func writeSeries(path string, points []Point) error {
	raw, err := json.Marshal(struct {
		Points []Point `json:"points"`
	}{Points: points})
	if err != nil {
		return err
	}
	return writeAtomic(path, raw)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".health-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
