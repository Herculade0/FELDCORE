package backup

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

const (
	jobQueued    = "queued"
	jobRunning   = "running"
	jobSucceeded = "succeeded"
	jobFailed    = "failed"

	maxJobLines = 1000
	jobTail     = 300
)

// Job is the view a page polls while a backup, check, or restore runs.
type Job struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	DestinationID string    `json:"destinationId"`
	Destination   string    `json:"destination"`
	State         string    `json:"state"`
	Step          string    `json:"step"`
	Percent       float64   `json:"percent"`
	BytesDone     int64     `json:"bytesDone"`
	BytesTotal    int64     `json:"bytesTotal"`
	FilesDone     int64     `json:"filesDone"`
	FilesTotal    int64     `json:"filesTotal"`
	SnapshotID    string    `json:"snapshotId,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	Error         string    `json:"error,omitempty"`
	Log           []string  `json:"log"`
	CreatedAt     time.Time `json:"createdAt"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
	FinishedAt    time.Time `json:"finishedAt,omitempty"`
	Actor         string    `json:"actor,omitempty"`
}

type bjob struct {
	Job
	tokenID  string
	readData bool
	snap     string
	paths    []string
	mode     string
	iaHead   string
	redact   []string
}

func (s *Service) jobsLocked() map[string]*bjob {
	if s.jobM == nil {
		s.jobM = map[string]*bjob{}
	}
	return s.jobM
}

func (j *bjob) view() Job {
	out := j.Job
	if len(j.Log) > jobTail {
		out.Log = append([]string(nil), j.Log[len(j.Log)-jobTail:]...)
	} else {
		out.Log = append([]string(nil), j.Log...)
	}
	if out.Log == nil {
		out.Log = []string{}
	}
	return out
}

func (s *Service) runningLike(destID, kind string) (*bjob, bool) {
	for _, id := range s.jobQ {
		j := s.jobM[id]
		if j == nil || j.DestinationID != destID {
			continue
		}
		if j.State != jobQueued && j.State != jobRunning {
			continue
		}
		if kind == "" || j.Kind == kind {
			return j, true
		}
	}
	return nil, false
}

func (s *Service) Jobs() []Job {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	out := make([]Job, 0, len(s.jobQ))
	for i := len(s.jobQ) - 1; i >= 0; i-- {
		if j := s.jobM[s.jobQ[i]]; j != nil {
			out = append(out, j.view())
		}
	}
	return out
}

func (s *Service) Job(id string) (Job, bool) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	j, ok := s.jobsLocked()[id]
	if !ok {
		return Job{}, false
	}
	return j.view(), true
}

func (s *Service) step(j *bjob, step string) {
	step = strings.TrimSpace(step)
	if step == "" {
		return
	}
	s.jobMu.Lock()
	j.Step = step
	s.jobMu.Unlock()
}

func (s *Service) logLine(j *bjob, line string) {
	line = strings.TrimSpace(secretsRedact(line, j.redact))
	if line == "" {
		return
	}
	if len(line) > 2000 {
		line = line[:2000]
	}
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if n := len(j.Log); n > 0 && j.Log[n-1] == line {
		return
	}
	j.Log = append(j.Log, line)
	if len(j.Log) > maxJobLines {
		j.Log = append([]string(nil), j.Log[len(j.Log)-maxJobLines:]...)
	}
}

func (s *Service) progress(j *bjob, p Progress) {
	if p.Err != "" {
		s.logLine(j, p.Err)
		return
	}
	s.jobMu.Lock()
	if p.Percent > j.Percent {
		j.Percent = p.Percent
	}
	if p.BytesDone > 0 {
		j.BytesDone = p.BytesDone
	}
	if p.BytesTotal > 0 {
		j.BytesTotal = p.BytesTotal
	}
	if p.FilesDone > 0 {
		j.FilesDone = p.FilesDone
	}
	if p.FilesTotal > 0 {
		j.FilesTotal = p.FilesTotal
	}
	if p.SnapshotID != "" {
		j.SnapshotID = p.SnapshotID
	}
	s.jobMu.Unlock()
	if p.Line != "" && !strings.HasPrefix(strings.TrimSpace(p.Line), "{") {
		s.logLine(j, p.Line)
	}
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
