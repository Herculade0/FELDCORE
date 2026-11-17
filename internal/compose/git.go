package compose

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/files"
)

var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

// ImportGit clones one public https repository and reads a compose file from
// it. The clone is deleted afterwards. Nothing is saved until the caller deploys.
func (s *Service) ImportGit(ctx context.Context, rawURL, ref, rel string, allowLAN bool) (Imported, error) {
	u, err := vetGitURL(ctx, rawURL, allowLAN)
	if err != nil {
		return Imported{}, err
	}
	ref = strings.TrimSpace(ref)
	if ref != "" {
		if !refRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasPrefix(ref, "-") {
			return Imported{}, inputf("The Git ref must be a branch, tag or commit name.")
		}
	}
	rel, err = cleanRel(rel)
	if err != nil {
		return Imported{}, err
	}
	root := s.Root()
	if err := os.MkdirAll(root, 0o750); err != nil {
		return Imported{}, opf("Could not prepare the stack folder.")
	}
	tmp, err := os.MkdirTemp(root, ".fetch-")
	if err != nil {
		return Imported{}, opf("Could not prepare a temporary folder for the clone.")
	}
	defer os.RemoveAll(tmp)
	dest := filepath.Join(tmp, "repo")
	args := []string{
		"-c", "protocol.file.allow=never",
		"-c", "http.followRedirects=false",
		"-c", "core.hooksPath=/dev/null",
		"clone", "--depth", "1", "--single-branch",
	}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", u.String(), dest)
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if _, err := s.run(ctx, command{
		Name: "git",
		Args: args,
		Env:  gitEnv(tmp),
		Dir:  tmp,
	}, nil); err != nil {
		return Imported{}, opf("Could not clone that repository. Check the address, the branch, and that the server can reach it.")
	}
	full, tried, err := findCompose(dest, rel)
	if err != nil {
		return Imported{}, err
	}
	body, err := readLimited(full, maxCompose)
	if err != nil {
		return Imported{}, err
	}
	envPath := filepath.Join(filepath.Dir(full), ".env")
	envText := ""
	if st, err := os.Stat(envPath); err == nil && !st.IsDir() {
		envText, err = readLimited(envPath, maxEnv)
		if err != nil {
			return Imported{}, err
		}
	}
	note := "Fetched " + tried + " from " + u.String() + "."
	if ref != "" {
		note = "Fetched " + tried + " from " + u.String() + " at " + ref + "."
	}
	return Imported{Compose: body, Env: envText, Path: tried, URL: u.String(), Ref: ref, Notes: []string{note}}, nil
}

func vetGitURL(ctx context.Context, raw string, allowLAN bool) (*url.URL, error) {
	u, err := files.ValidateURL(raw)
	if err != nil {
		return nil, &InputError{Msg: err.Error()}
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, inputf("Use an https Git address.")
	}
	host := u.Hostname()
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(ips) == 0 {
		return nil, inputf("That Git host could not be resolved.")
	}
	for _, ip := range ips {
		if err := files.CheckIP(ip.IP, allowLAN); err != nil {
			return nil, &InputError{Msg: err.Error()}
		}
	}
	return u, nil
}

func cleanRel(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "docker-compose.yml", nil
	}
	if strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || filepath.IsAbs(p) {
		return "", inputf("The path inside the repository must be relative.")
	}
	p = pathClean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", inputf("The path has to stay inside the repository.")
	}
	return p, nil
}

func pathClean(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	var parts []string
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "", ".":
		case "..":
			if len(parts) == 0 {
				return "../" + p
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

func findCompose(root, rel string) (string, string, error) {
	try := []string{rel}
	if rel == "docker-compose.yml" {
		try = append(try, "compose.yaml", "docker-compose.yaml", "compose.yml")
	}
	for _, name := range try {
		full := filepath.Join(root, filepath.FromSlash(name))
		check, err := filepath.Rel(root, full)
		if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
			return "", "", inputf("The path has to stay inside the repository.")
		}
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			continue
		}
		return full, name, nil
	}
	return "", "", inputf("That path is not a compose file in the repository.")
}

func readLimited(path string, max int) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", inputf("That file could not be read.")
	}
	if st.Size() > int64(max) {
		return "", inputf("That file is too large to import.")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", inputf("That file could not be read.")
	}
	if len(b) > max {
		return "", inputf("That file is too large to import.")
	}
	return string(b), nil
}

func gitEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_COUNT=0",
		"GCM_INTERACTIVE=never",
	}
}
