package compose

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// command is one process. A non-nil Env replaces the environment.
type command struct {
	Dir  string
	Name string
	Args []string
	Env  []string
}

// Exec runs a command and streams each output line. Tests replace it.
type Exec func(ctx context.Context, cmd command, line func(string)) (string, error)

// Log is the job runner's progress reporter. A nil Log drops the lines.
type Log interface {
	Step(name string)
	Line(text string)
}

func runCommand(ctx context.Context, cmd command, line func(string)) (string, error) {
	path, err := exec.LookPath(cmd.Name)
	if err != nil {
		return "", fmt.Errorf("%s is not installed", cmd.Name)
	}
	c := exec.CommandContext(ctx, path, cmd.Args...)
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}
	if cmd.Env != nil {
		c.Env = cmd.Env
	} else {
		c.Env = append(os.Environ(), "LC_ALL=C", "TERM=dumb", "DOCKER_CLI_HINTS=false")
	}
	if line != nil {
		line("$ " + cmd.Name + " " + strings.Join(cmd.Args, " "))
	}
	text, err := stream(c, line)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return text, context.DeadlineExceeded
	}
	if err != nil {
		tail := lastLine(text)
		if tail == "" {
			return text, err
		}
		return text, fmt.Errorf("%s", tail)
	}
	return text, nil
}

func stream(c *exec.Cmd, line func(string)) (string, error) {
	pr, pw := io.Pipe()
	c.Stdout = pw
	c.Stderr = pw
	if err := c.Start(); err != nil {
		_ = pw.Close()
		return "", err
	}
	var wg sync.WaitGroup
	var waitErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr = c.Wait()
		_ = pw.Close()
	}()
	text := readOut(pr, line)
	wg.Wait()
	return text, waitErr
}

func readOut(r io.Reader, line func(string)) string {
	br := bufio.NewReaderSize(r, 32*1024)
	var all strings.Builder
	var cur []byte
	flush := func() {
		text := strings.TrimSpace(string(cur))
		cur = cur[:0]
		if text == "" {
			return
		}
		if len(text) > 2000 {
			text = text[:2000]
		}
		all.WriteString(text)
		all.WriteByte('\n')
		if line != nil {
			line(text)
		}
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			break
		}
		if b == '\n' || b == '\r' {
			flush()
			continue
		}
		if len(cur) < 8192 {
			cur = append(cur, b)
		}
	}
	flush()
	return strings.TrimSpace(all.String())
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

func (s *Service) run(ctx context.Context, cmd command, line func(string)) (string, error) {
	fn := s.exec
	if fn == nil {
		fn = runCommand
	}
	return fn(ctx, cmd, line)
}

func lineOf(log Log) func(string) {
	if log == nil {
		return nil
	}
	return log.Line
}

func step(log Log, name string) {
	if log != nil && strings.TrimSpace(name) != "" {
		log.Step(name)
	}
}

// InstallCLI installs the Compose v2 plugin with apt when it is missing.
// It is registered on the shared install job runner.
func InstallCLI(ctx context.Context) error {
	if ver, err := probeVersion(ctx); err == nil {
		install.Line(ctx, "Docker Compose "+ver+" is already installed.")
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return opf("Automatic install needs apt. Install the docker-compose-v2 package, then reload this page.")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return opf("Install Docker first. Compose is a plugin for the docker command.")
	}
	install.Line(ctx, "Installing Docker Compose.")
	if _, err := install.Run(ctx, 3*time.Minute, "apt-get", "update"); err != nil {
		install.Line(ctx, "Package lists could not be refreshed. Trying the install anyway.")
	}
	var last error
	for _, pkg := range []string{"docker-compose-v2", "docker-compose-plugin"} {
		if _, err := install.Run(ctx, 8*time.Minute, "apt-get", "install", "-y", pkg); err != nil {
			last = err
			continue
		}
		if _, err := probeVersion(ctx); err == nil {
			install.Line(ctx, "Docker Compose is installed.")
			return nil
		}
	}
	if last != nil {
		return opf("Could not install Docker Compose: %s", last.Error())
	}
	return opf("Docker Compose was not installed. Install the docker-compose-v2 package, then try again.")
}

func probeVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := runCommand(ctx, command{Name: "docker", Args: []string{"compose", "version", "--short"}}, nil)
	if err != nil {
		return "", err
	}
	line := lastLine(out)
	if line == "" {
		return "", fmt.Errorf("docker compose did not report a version")
	}
	return line, nil
}
