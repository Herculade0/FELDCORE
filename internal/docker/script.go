package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

// RunScript starts a short-lived container, runs script with sh -c, and
// returns its logs. The container is removed before RunScript returns.
// Logs can contain a secret. Callers must not write them to the log.
func (m *Manager) RunScript(ctx context.Context, image string, binds []string, script string) (string, error) {
	image = strings.TrimSpace(image)
	script = strings.TrimSpace(script)
	if script == "" {
		return "", invalid("There is no command to run.")
	}
	if !imageRe.MatchString(image) {
		return "", invalid("That image name is not valid.")
	}
	for _, b := range binds {
		if strings.TrimSpace(b) == "" || strings.Contains(b, "\n") {
			return "", invalid("A data folder for that command is not valid.")
		}
	}
	var nonce [3]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	name := "feldcore-bridge-info-" + hex.EncodeToString(nonce[:])
	body := map[string]any{
		"Image":      image,
		"Entrypoint": []string{"sh", "-c"},
		"Cmd":        []string{script},
		"Tty":        false,
		"Labels": map[string]string{
			ManagedLabel: "true",
			RoleLabel:    "proton-bridge-info",
		},
		"HostConfig": map[string]any{
			"Binds":         binds,
			"NetworkMode":   "none",
			"RestartPolicy": map[string]any{"Name": "no", "MaximumRetryCount": 0},
		},
	}
	id, err := m.createContainer(ctx, name, body)
	if err != nil {
		return "", err
	}
	defer func() {
		rmCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = m.c.do2(rmCtx, "DELETE", "/containers/"+esc(id), url.Values{"force": {"1"}})
	}()
	if err := m.c.postJSON(ctx, "/containers/"+esc(id)+"/start", nil, nil, nil); err != nil {
		return "", err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var waited struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := m.c.postJSON(waitCtx, "/containers/"+esc(id)+"/wait", nil, nil, &waited); err != nil {
		killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.c.postJSON(killCtx, "/containers/"+esc(id)+"/kill", nil, nil, nil)
		killCancel()
		logs, logErr := m.Logs(context.Background(), id, 200)
		if logErr != nil || logs == "" {
			return "", err
		}
		return logs, err
	}
	logs, err := m.Logs(ctx, id, 200)
	if err != nil {
		return "", err
	}
	if waited.StatusCode != 0 && !strings.Contains(logs, "Username:") && !strings.Contains(logs, "username:") {
		return logs, &OpError{Msg: "Bridge did not print a mailbox."}
	}
	return logs, nil
}
