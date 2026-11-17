package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
)

// Version reports the Compose v2 CLI version.
func (s *Service) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := s.run(ctx, command{Name: "docker", Args: []string{"compose", "version", "--short"}}, nil)
	if err != nil {
		return "", err
	}
	line := lastLine(out)
	if line == "" {
		return "", fmt.Errorf("docker compose did not report a version")
	}
	return line, nil
}

// Deploy creates the stack network and runs compose up.
func (s *Service) Deploy(ctx context.Context, name string, log Log) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	if _, err := s.Version(ctx); err != nil {
		return opf("Docker Compose v2 is not installed. Install it from this page, then deploy again.")
	}
	st, err := s.read(name)
	if err != nil {
		return err
	}
	step(log, "Preparing "+name)
	ov, network, err := Override(st.Compose, name, st.Isolate)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.dir(name), overrideFile), ov, 0o640); err != nil {
		return opf("Could not write the network override.")
	}
	if strings.HasPrefix(network, docker.StackNetworkPrefix) {
		step(log, "Creating the private network")
		if err := s.ensureNetwork(ctx, log, network, name); err != nil {
			s.setState(name, "failed")
			return err
		}
	}
	if st.Replaces != "" {
		step(log, "Replacing the original container")
		if log != nil {
			log.Line("Stopping the container this stack was converted from. Its mapped folders stay on disk.")
		}
		if err := s.removeOriginal(ctx, st.Replaces); err != nil {
			s.setState(name, "failed")
			return err
		}
		s.clearReplaces(name)
	}
	step(log, "Starting the stack")
	if _, err := s.compose(ctx, log, name, 15*time.Minute, "up", "-d", "--remove-orphans"); err != nil {
		s.setState(name, "failed")
		return opf("The stack did not start: %s", cleanErr(err))
	}
	s.setState(name, "running")
	if log != nil {
		log.Line(name + " is up.")
	}
	return nil
}

// Update pulls images and recreates the containers.
func (s *Service) Update(ctx context.Context, name string, log Log) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	if _, err := s.Version(ctx); err != nil {
		return opf("Docker Compose v2 is not installed. Install it from this page, then update again.")
	}
	if _, err := s.read(name); err != nil {
		return err
	}
	step(log, "Pulling images and recreating containers")
	_, err = s.compose(ctx, log, name, 20*time.Minute, "up", "-d", "--pull", "always", "--remove-orphans")
	if err != nil && unknownPull(err) {
		if log != nil {
			log.Line("This Compose build has no --pull always. Pulling, then starting.")
		}
		if _, pullErr := s.compose(ctx, log, name, 15*time.Minute, "pull"); pullErr != nil && log != nil {
			log.Line("Some images were not pulled: " + cleanErr(pullErr))
		}
		_, err = s.compose(ctx, log, name, 15*time.Minute, "up", "-d", "--remove-orphans")
	}
	if err != nil {
		s.setState(name, "failed")
		return opf("The stack was not updated: %s", cleanErr(err))
	}
	s.setState(name, "running")
	return nil
}

// Stop stops the stack's containers and leaves the files in place.
func (s *Service) Stop(ctx context.Context, name string, log Log) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	if _, err := s.read(name); err != nil {
		return err
	}
	step(log, "Stopping "+name)
	if _, err := s.compose(ctx, log, name, 3*time.Minute, "stop"); err != nil {
		return opf("The stack did not stop: %s", cleanErr(err))
	}
	s.setState(name, "stopped")
	return nil
}

// Remove runs compose down and deletes the stack directory.
// volumes removes named volumes only when the caller asked.
func (s *Service) Remove(ctx context.Context, name string, volumes bool, log Log) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	st, err := s.read(name)
	if err != nil {
		return err
	}
	step(log, "Removing "+name)
	args := []string{"down", "--remove-orphans"}
	if volumes {
		args = append(args, "--volumes")
		if log != nil {
			log.Line("Named volumes for this stack will be deleted. Mapped folders on the server stay.")
		}
	}
	if _, err := s.Version(ctx); err == nil {
		if _, err := s.compose(ctx, log, name, 4*time.Minute, args...); err != nil && log != nil {
			log.Line("Compose down reported: " + cleanErr(err))
		}
	}
	if strings.HasPrefix(st.Network, docker.StackNetworkPrefix) {
		s.removeNetwork(ctx, st.Network)
	}
	if err := os.RemoveAll(s.dir(name)); err != nil {
		return opf("The containers were removed, but the stack files could not be deleted.")
	}
	return nil
}

// Logs returns the recent output of the stack, or of one service.
func (s *Service) Logs(ctx context.Context, name, service string, tail int) (string, error) {
	name, err := CleanName(name)
	if err != nil {
		return "", err
	}
	st, err := s.read(name)
	if err != nil {
		return "", err
	}
	if service != "" {
		if !svcNameRe.MatchString(service) || !hasService(st.Compose, service) {
			return "", inputf("That service is not in this stack.")
		}
	}
	if tail < 1 {
		tail = 200
	}
	if tail > maxLogTail {
		tail = maxLogTail
	}
	args := []string{"logs", "--no-color", "--tail", fmt.Sprintf("%d", tail)}
	if service != "" {
		args = append(args, service)
	}
	out, err := s.compose(ctx, nil, name, 20*time.Second, args...)
	if err != nil {
		if out != "" {
			return out, nil
		}
		return "", opf("Could not read the logs: %s", cleanErr(err))
	}
	return out, nil
}

// FromContainer writes a stack from one existing container. Deploying it
// removes that container when removeOriginal is set. Volumes on the host stay.
func (s *Service) FromContainer(ctx context.Context, id, name string, removeOriginal bool) (Stack, error) {
	if s.inspect == nil {
		return Stack{}, opf("Docker is not available.")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Stack{}, inputf("Choose a container.")
	}
	info, err := s.inspect(ctx, id)
	if err != nil {
		return Stack{}, err
	}
	if !info.Editable && strings.Contains(strings.ToLower(info.Reason), "compose") {
		return Stack{}, &docker.InputError{Msg: info.Reason}
	}
	body, notes, err := FromSpec(info.Spec)
	if err != nil {
		return Stack{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = suggestName(info.Spec.Name)
	}
	name, err = CleanName(name)
	if err != nil {
		return Stack{}, err
	}
	replaces := ""
	if removeOriginal {
		replaces = id
	}
	st, err := s.Create(Write{
		Name: name, Compose: body, Isolate: true, Source: "container", Replaces: replaces,
	})
	if err != nil {
		return Stack{}, err
	}
	st.Notes = notes
	return st, nil
}

func suggestName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = strings.Trim("app-"+out, "-")
	}
	if len(out) > 31 {
		out = strings.Trim(out[:31], "-")
	}
	if out == "" {
		out = "app"
	}
	return out
}

func (s *Service) compose(ctx context.Context, log Log, name string, timeout time.Duration, args ...string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	dir := s.dir(name)
	full := []string{
		"compose", "--project-name", Project(name), "--project-directory", dir,
		"-f", filepath.Join(dir, composeFile),
		"-f", filepath.Join(dir, overrideFile),
	}
	if st, err := os.Stat(filepath.Join(dir, envFile)); err == nil && !st.IsDir() {
		full = append(full, "--env-file", filepath.Join(dir, envFile))
	}
	full = append(full, args...)
	return s.run(ctx, command{Name: "docker", Dir: dir, Args: full}, lineOf(log))
}

func (s *Service) ensureNetwork(ctx context.Context, log Log, network, stack string) error {
	if !strings.HasPrefix(network, docker.StackNetworkPrefix) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := s.run(ctx, command{Name: "docker", Args: []string{"network", "inspect", network}}, nil); err == nil {
		if log != nil {
			log.Line("Network " + network + " already exists.")
		}
		return nil
	}
	_, err := s.run(ctx, command{Name: "docker", Args: []string{
		"network", "create", "--driver", "bridge",
		"--label", docker.ManagedLabel + "=true",
		"--label", docker.StackLabel + "=" + stack,
		"--label", "feldcore.network=stack",
		network,
	}}, lineOf(log))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return opf("Could not create the network %s: %s", network, cleanErr(err))
	}
	return nil
}

func (s *Service) removeNetwork(ctx context.Context, network string) {
	if !strings.HasPrefix(network, docker.StackNetworkPrefix) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, _ = s.run(ctx, command{Name: "docker", Args: []string{"network", "rm", network}}, nil)
}

func (s *Service) removeOriginal(ctx context.Context, id string) error {
	if s.remove == nil {
		return opf("The original container could not be removed.")
	}
	if err := s.remove(ctx, id); err != nil {
		if docker.NotFound(err) {
			return nil
		}
		return opf("The original container could not be removed: %s", cleanErr(err))
	}
	return nil
}

func (s *Service) ps(ctx context.Context, name string) ([]LiveService, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := s.compose(ctx, nil, name, 0, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePS(out), nil
}

type psRow struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	Service    string `json:"Service"`
	State      string `json:"State"`
	Status     string `json:"Status"`
	Image      string `json:"Image"`
	Publishers []struct {
		TargetPort    int    `json:"TargetPort"`
		PublishedPort int    `json:"PublishedPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

func parsePS(out string) []LiveService {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" || out == "[]" {
		return nil
	}
	var rows []psRow
	if strings.HasPrefix(out, "[") {
		_ = json.Unmarshal([]byte(out), &rows)
	} else {
		dec := json.NewDecoder(strings.NewReader(out))
		for {
			var row psRow
			if err := dec.Decode(&row); err != nil {
				break
			}
			if row.Service != "" || row.Name != "" {
				rows = append(rows, row)
			}
		}
	}
	var live []LiveService
	for _, row := range rows {
		item := LiveService{
			Name: row.Service, Image: row.Image, Container: row.Name,
			ID: row.ID, State: strings.ToLower(row.State), Status: row.Status,
			Ports: []string{},
		}
		if item.Name == "" {
			item.Name = row.Name
		}
		seenPort := map[string]bool{}
		for _, p := range row.Publishers {
			if p.PublishedPort <= 0 {
				continue
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			// Compose reports one publisher per address, so IPv4 and IPv6
			// would otherwise show the same port twice.
			label := fmt.Sprintf("%d→%d/%s", p.PublishedPort, p.TargetPort, proto)
			if seenPort[label] {
				continue
			}
			seenPort[label] = true
			item.Ports = append(item.Ports, label)
		}
		live = append(live, item)
	}
	return live
}

func hasService(compose, name string) bool {
	f, err := parse(compose)
	if err != nil {
		return false
	}
	for _, s := range f.services {
		if s.name == name {
			return true
		}
	}
	return false
}

func unknownPull(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown flag") || strings.Contains(msg, "unknown shorthand")
}

func cleanErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "it took too long"
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
