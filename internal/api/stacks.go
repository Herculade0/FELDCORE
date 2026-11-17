package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/compose"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/install"
)

func (s *Server) haveStacks(w http.ResponseWriter) bool {
	if s.stacks == nil {
		writeError(w, http.StatusNotFound, "Stacks are not available.")
		return false
	}
	return true
}

func isolateChoice(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

type stackFile struct {
	Name         string        `json:"name"`
	Compose      string        `json:"compose"`
	Env          string        `json:"env"`
	Isolate      *bool         `json:"isolate"`
	Source       string        `json:"source"`
	GitURL       string        `json:"gitUrl"`
	GitPath      string        `json:"gitPath"`
	GitRef       string        `json:"gitRef"`
	Replaces     string        `json:"replaces"`
	Deploy       bool          `json:"deploy"`
	Confirm      bool          `json:"confirm"`
	DeployAnyway bool          `json:"deployAnyway"`
	Rewrite      bool          `json:"rewrite"`
	Fixes        []compose.Fix `json:"fixes"`
}

func (s *Server) handleStacks(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	cat, err := s.stacks.Catalog(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) handleStackGet(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	st, err := s.stacks.Get(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStackPreview(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	var body stackFile
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	p, err := s.stacks.Preview(body.Name, body.Compose, body.Env, isolateChoice(body.Isolate, true), body.Rewrite, body.Fixes)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleStackDiff(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	var body stackFile
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	body.Name = chi.URLParam(r, "name")
	p, err := s.stacks.Preview(body.Name, body.Compose, body.Env, isolateChoice(body.Isolate, true), false, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleStackCreate(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	var body stackFile
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	if body.Deploy {
		if !s.acceptStack(w, r, "stack.deploy", body.Name, body.Compose, body.Env, isolateChoice(body.Isolate, true), body.Confirm, body.DeployAnyway) {
			return
		}
	}
	st, err := s.stacks.Create(compose.Write{
		Name: body.Name, Compose: body.Compose, Env: body.Env,
		Isolate: isolateChoice(body.Isolate, true), Source: body.Source,
		GitURL: body.GitURL, GitPath: body.GitPath, GitRef: body.GitRef,
		Replaces: body.Replaces,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "stack.create", st.Name, "ok")
	if !body.Deploy {
		writeJSON(w, http.StatusCreated, st)
		return
	}
	s.startStackJob(w, r, st.Name, "deploy", "Deploy "+st.Name, func(ctx context.Context, log compose.Log) error {
		return s.stacks.Deploy(ctx, st.Name, log)
	})
}

func (s *Server) handleStackSave(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	name := chi.URLParam(r, "name")
	var body stackFile
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	current, err := s.stacks.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	src := body.Source
	if src == "" {
		src = current.Source
	}
	gitURL, gitPath, gitRef := body.GitURL, body.GitPath, body.GitRef
	if gitURL == "" {
		gitURL, gitPath, gitRef = current.GitURL, current.GitPath, current.GitRef
	}
	st, err := s.stacks.Save(compose.Write{
		Name: name, Compose: body.Compose, Env: body.Env,
		Isolate: isolateChoice(body.Isolate, current.Isolate), Source: src,
		GitURL: gitURL, GitPath: gitPath, GitRef: gitRef,
		Replaces: current.Replaces,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "stack.save", st.Name, "ok")
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStackDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	name := chi.URLParam(r, "name")
	var body stackFile
	if !readJSONLimit(w, r, &body, 1<<20) {
		return
	}
	current, err := s.stacks.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	composeText, envText, isolate := current.Compose, current.Env, current.Isolate
	if strings.TrimSpace(body.Compose) != "" {
		composeText = body.Compose
		envText = body.Env
		isolate = isolateChoice(body.Isolate, current.Isolate)
	}
	if !s.acceptStack(w, r, "stack.deploy", name, composeText, envText, isolate, body.Confirm, body.DeployAnyway) {
		return
	}
	if strings.TrimSpace(body.Compose) != "" {
		src := body.Source
		if src == "" {
			src = current.Source
		}
		if _, err := s.stacks.Save(compose.Write{
			Name: name, Compose: composeText, Env: envText, Isolate: isolate, Source: src,
			GitURL: current.GitURL, GitPath: current.GitPath, GitRef: current.GitRef,
			Replaces: current.Replaces,
		}); err != nil {
			s.fail(w, r, err)
			return
		}
		s.auditFile(r, "stack.save", name, "ok")
	}
	s.startStackJob(w, r, name, "deploy", "Deploy "+name, func(ctx context.Context, log compose.Log) error {
		return s.stacks.Deploy(ctx, name, log)
	})
}

func (s *Server) handleStackUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	name := chi.URLParam(r, "name")
	var body struct {
		DeployAnyway bool `json:"deployAnyway"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	current, err := s.stacks.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.acceptStack(w, r, "stack.update", name, current.Compose, current.Env, current.Isolate, true, body.DeployAnyway) {
		return
	}
	s.startStackJob(w, r, name, "update", "Update "+name, func(ctx context.Context, log compose.Log) error {
		return s.stacks.Update(ctx, name, log)
	})
}

func (s *Server) handleStackStop(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	name := chi.URLParam(r, "name")
	if _, err := s.stacks.Get(r.Context(), name); err != nil {
		s.fail(w, r, err)
		return
	}
	s.startStackJob(w, r, name, "stop", "Stop "+name, func(ctx context.Context, log compose.Log) error {
		return s.stacks.Stop(ctx, name, log)
	})
}

func (s *Server) handleStackRemove(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	name := chi.URLParam(r, "name")
	volumes := r.URL.Query().Get("volumes") == "1" || r.URL.Query().Get("volumes") == "true"
	if _, err := s.stacks.Get(r.Context(), name); err != nil {
		s.fail(w, r, err)
		return
	}
	s.startStackJob(w, r, name, "remove", "Remove "+name, func(ctx context.Context, log compose.Log) error {
		return s.stacks.Remove(ctx, name, volumes, log)
	})
}

func (s *Server) handleStackLogs(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	out, err := s.stacks.Logs(r.Context(), chi.URLParam(r, "name"), r.URL.Query().Get("service"), tail)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"log": out})
}

func (s *Server) handleStackJob(w http.ResponseWriter, r *http.Request) {
	if s.installs == nil {
		writeError(w, http.StatusNotFound, "That job is no longer on this server.")
		return
	}
	job, ok := s.installs.Get(chi.URLParam(r, "id"))
	if !ok || !strings.HasPrefix(job.Name, "stack:") {
		writeError(w, http.StatusNotFound, "That job is no longer on this server.")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleStackImport(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	var body struct {
		URL     string `json:"url"`
		Ref     string `json:"ref"`
		Path    string `json:"path"`
		Rewrite bool   `json:"rewrite"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	allow := s.auth != nil && s.auth.AllowLANURLs()
	got, err := s.stacks.ImportGit(r.Context(), body.URL, body.Ref, body.Path, allow)
	if err != nil {
		var input *compose.InputError
		if !errors.As(err, &input) {
			s.auditFile(r, "stack.import", body.URL, "error")
		}
		s.fail(w, r, err)
		return
	}
	if body.Rewrite {
		p, err := s.stacks.Preview("", got.Compose, got.Env, true, true, nil)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if p.Valid && p.Changed {
			got.Compose = p.Compose
			got.Notes = append(got.Notes, p.Notes...)
		}
	}
	s.auditFile(r, "stack.import", got.URL, "ok")
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleStackFromContainer(w http.ResponseWriter, r *http.Request) {
	if !s.haveStacks(w) {
		return
	}
	var body struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		RemoveOriginal *bool  `json:"removeOriginal"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	remove := true
	if body.RemoveOriginal != nil {
		remove = *body.RemoveOriginal
	}
	st, err := s.stacks.FromContainer(r.Context(), body.ID, body.Name, remove)
	if err != nil {
		var input *compose.InputError
		var dockerInput *docker.InputError
		if !errors.As(err, &input) && !errors.As(err, &dockerInput) {
			s.auditFile(r, "stack.convert", body.ID, "error")
		}
		s.fail(w, r, err)
		return
	}
	s.auditFile(r, "stack.convert", st.Name, "ok")
	writeJSON(w, http.StatusCreated, st)
}

// acceptStack runs the deploy gate. deployAnyway only counts for an
// administrator. A lesser caller who would need that acknowledgement is refused.
func (s *Server) acceptStack(w http.ResponseWriter, r *http.Request, action, name, composeText, envText string, isolate, confirm, anyway bool) bool {
	admin := s.isAdmin(r)
	if _, err := s.stacks.Check(name, composeText, envText, isolate, confirm, anyway && admin); err != nil {
		var gate *compose.Gate
		if errors.As(err, &gate) && gate.Ack && !admin {
			s.auditFile(r, action, name, "denied")
			writeError(w, http.StatusForbidden, "High-risk settings need an administrator.")
			return false
		}
		if s.refusedGate(w, r, action, name, err) {
			return false
		}
		s.fail(w, r, err)
		return false
	}
	return true
}

// refusedGate writes a 409 and audits the refusal. Other errors return false
// so the caller can map them.
func (s *Server) refusedGate(w http.ResponseWriter, r *http.Request, action, name string, err error) bool {
	var gate *compose.Gate
	if !errors.As(err, &gate) {
		return false
	}
	s.auditFile(r, action, name, "denied")
	s.fail(w, r, err)
	return true
}

func (s *Server) startStackJob(w http.ResponseWriter, r *http.Request, name, verb, label string, run func(context.Context, compose.Log) error) {
	if s.installs == nil {
		writeError(w, http.StatusNotFound, "Jobs are not available.")
		return
	}
	actor, tokenID := actorOf(r)
	target := name
	if verb == "remove" && (r.URL.Query().Get("volumes") == "1" || r.URL.Query().Get("volumes") == "true") {
		target = name + " (with volumes)"
	}
	job, err := s.installs.StartSpec("stack:"+name+":"+verb, install.Spec{
		Label: label,
		Quiet: true,
		Run: func(ctx context.Context, rep install.Reporter) error {
			err := run(ctx, rep)
			result := "ok"
			if err != nil {
				result = "error"
			}
			s.auditNamed(actor, tokenID, "stack."+verb, target, result)
			return err
		},
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
