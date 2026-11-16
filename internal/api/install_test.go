package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

func TestInstallJobSurvivesAndLocks(t *testing.T) {
	srv, _ := newAgentServer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	srv.installs.Register("libvirt", install.Spec{
		Label: "libvirt / KVM",
		Apt:   true,
		Run: func(ctx context.Context, rep install.Reporter) error {
			if calls.Add(1) == 1 {
				rep.Step("Installing packages")
				rep.Line("Unpacking libvirt-daemon")
				close(entered)
				<-release
			}
			return nil
		},
	})
	cookie := loginCookie(t, srv)

	empty := call(srv, http.MethodGet, "/api/v1/installs?name=libvirt", "", "", cookie)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"job":null`) {
		t.Fatalf("empty latest: %d %s", empty.Code, empty.Body.String())
	}
	if rec := call(srv, http.MethodPost, "/api/v1/installs", `{"name":"nope"}`, "", cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
	}

	first := call(srv, http.MethodPost, "/api/v1/installs", `{"name":"libvirt"}`, "", cookie)
	if first.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", first.Code, first.Body.String())
	}
	var job install.Job
	if err := json.Unmarshal(first.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	second := call(srv, http.MethodPost, "/api/v1/installs", `{"name":"libvirt"}`, "", cookie)
	var again install.Job
	if err := json.Unmarshal(second.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != job.ID {
		t.Fatalf("double start ids %s and %s", job.ID, again.ID)
	}
	<-entered
	if calls.Load() != 1 {
		t.Fatalf("apt started %d times", calls.Load())
	}

	got := call(srv, http.MethodGet, "/api/v1/installs/"+job.ID, "", "", cookie)
	if got.Code != http.StatusOK {
		t.Fatalf("get: %d %s", got.Code, got.Body.String())
	}
	var polled install.Job
	if err := json.Unmarshal(got.Body.Bytes(), &polled); err != nil {
		t.Fatal(err)
	}
	if polled.State != install.Running || polled.StartedAt.IsZero() {
		t.Fatalf("polled %+v", polled)
	}
	if !strings.Contains(strings.Join(polled.Log, "\n"), "Unpacking libvirt-daemon") {
		t.Fatalf("log %v", polled.Log)
	}
	latest := call(srv, http.MethodGet, "/api/v1/installs?name=libvirt", "", "", cookie)
	if !strings.Contains(latest.Body.String(), job.ID) {
		t.Fatalf("latest did not resume %s: %s", job.ID, latest.Body.String())
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	var finished install.Job
	for time.Now().Before(deadline) {
		rec := call(srv, http.MethodGet, "/api/v1/installs/"+job.ID, "", "", cookie)
		_ = json.Unmarshal(rec.Body.Bytes(), &finished)
		if finished.State == install.Succeeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.State != install.Succeeded || finished.FinishedAt.IsZero() {
		t.Fatalf("finished %+v", finished)
	}
	var started, done bool
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(started && done) {
		started, done = false, false
		for _, ev := range srv.auth.Audit() {
			if ev.Target != "libvirt" || ev.TokenName != "admin" {
				continue
			}
			if ev.Action == "install started" && ev.Result == "ok" {
				started = true
			}
			if ev.Action == "install finished" && ev.Result == "ok" {
				done = true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started || !done {
		t.Fatalf("audit %+v", srv.auth.Audit())
	}
}
