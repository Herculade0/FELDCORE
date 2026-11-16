package docker

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"time"
)

// ContainerHealth is the slice of a container the health page needs.
// Health is the healthcheck status when Docker has one ("healthy",
// "unhealthy", "starting"), otherwise the human status text.
type ContainerHealth struct {
	Name         string
	State        string
	Status       string
	Health       string
	RestartCount int
	OOMKilled    bool
}

// RuntimeHealth lists containers and their health without the live stat
// lookups the container page does. A missing daemon is returned as an error
// so the caller can say Docker is not running instead of inventing a failure.
func (m *Manager) RuntimeHealth(ctx context.Context) ([]ContainerHealth, error) {
	if m == nil || m.c == nil {
		return nil, errNotConfigured
	}
	var raw []apiContainer
	if err := m.c.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
		return nil, err
	}
	if len(raw) > 200 {
		raw = raw[:200]
	}
	out := make([]ContainerHealth, len(raw))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, rc := range raw {
		out[i] = ContainerHealth{
			Name:   cleanName(rc.Names),
			State:  rc.State,
			Status: rc.Status,
			Health: rc.Status,
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			ictx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			body, err := m.c.getRaw(ictx, "/containers/"+esc(id)+"/json")
			if err != nil {
				return
			}
			var in struct {
				State struct {
					OOMKilled    bool `json:"OOMKilled"`
					RestartCount int  `json:"RestartCount"`
					Health       *struct {
						Status string `json:"Status"`
					} `json:"Health"`
				} `json:"State"`
			}
			if json.Unmarshal(body, &in) != nil {
				return
			}
			out[i].OOMKilled = in.State.OOMKilled
			out[i].RestartCount = in.State.RestartCount
			if in.State.Health != nil && in.State.Health.Status != "" {
				out[i].Health = in.State.Health.Status
			}
		}(i, rc.ID)
	}
	wg.Wait()
	return out, nil
}

var errNotConfigured = errString("docker is not configured")

type errString string

func (e errString) Error() string { return string(e) }
