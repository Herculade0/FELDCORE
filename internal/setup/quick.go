package setup

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/vpn"
)

const secretNtfy = "ntfy-token"

var ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *Service) applyRemote(ctx context.Context, req Request) error {
	mode := strings.TrimSpace(req.RemoteMode)
	if mode == "" {
		mode = "none"
	}
	switch mode {
	case "none":
		if s.tunnel != nil {
			if err := s.tunnel.Save(ctx, remote.SaveInput{Mode: "off"}); err != nil {
				return err
			}
		}
		return nil
	case "cloudflare":
		if s.tunnel == nil {
			return &OpError{"Remote access is not available on this server."}
		}
		cf := strings.TrimSpace(req.CFMode)
		if cf == "" {
			cf = "token"
		}
		return s.tunnel.Save(ctx, remote.SaveInput{
			Mode:        cf,
			TunnelToken: req.CFToken,
			APIToken:    req.CFAPI,
			TunnelName:  "feldcore",
		})
	case "wireguard":
		if s.vpn == nil {
			return &OpError{"WireGuard is not available on this server."}
		}
		if strings.TrimSpace(req.WGConf) == "" {
			return &InputError{"Paste a WireGuard configuration, or choose none."}
		}
		on := true
		if _, err := s.vpn.Save(ctx, vpn.SaveRequest{
			Name:    "Home",
			Preset:  "auto",
			Conf:    req.WGConf,
			Enabled: &on,
		}); err != nil {
			return err
		}
		return s.vpn.SetEnabled(ctx, true)
	default:
		return &InputError{"Choose none, Cloudflare Tunnel, or WireGuard."}
	}
}

func (s *Service) applyNotify(ctx context.Context, req Request) error {
	kind := strings.TrimSpace(req.NotifyKind)
	if kind == "" || kind == "none" {
		return nil
	}
	switch kind {
	case "email":
		if s.mail == nil {
			return &OpError{"Email notifications are not available on this server."}
		}
		port := req.MailPort
		if port == 0 {
			port = 587
		}
		tlsMode := req.MailTLS
		if tlsMode == "" {
			tlsMode = "starttls"
		}
		if err := s.mail.Save(ctx, notify.Settings{
			Enabled: true, Mode: notify.ModeSMTP,
			Host: req.MailHost, Port: port, Username: req.MailUser,
			From: req.MailFrom, To: req.MailTo, TLS: tlsMode, Password: req.MailPass,
			OnContainerStop: true, OnUpdate: true,
		}); err != nil {
			return err
		}
		if err := s.mail.Test(ctx); err != nil {
			return err
		}
		return nil
	case "ntfy":
		return s.applyNtfy(ctx, req)
	default:
		return &InputError{"Choose email, ntfy, or skip notifications."}
	}
}

func (s *Service) applyNtfy(ctx context.Context, req Request) error {
	base := strings.TrimRight(strings.TrimSpace(req.NtfyURL), "/")
	if base == "" {
		base = "https://ntfy.sh"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return &InputError{"Enter an http or https ntfy server, for example https://ntfy.sh."}
	}
	topic := strings.TrimSpace(req.NtfyTopic)
	if !ntfyTopic.MatchString(topic) {
		return &InputError{"Topic names use letters, numbers, hyphens and underscores."}
	}
	if tok := strings.TrimSpace(req.NtfyToken); tok != "" {
		if s.box == nil {
			return &OpError{"Could not store the ntfy token."}
		}
		if err := s.box.Put(secretNtfy, tok); err != nil {
			return &OpError{"Could not store the ntfy token."}
		}
	}
	token := strings.TrimSpace(req.NtfyToken)
	if token == "" && s.box != nil {
		if saved, ok, err := s.box.Get(secretNtfy); err == nil && ok {
			token = saved
		}
	}
	if err := publishNtfy(ctx, base, topic, token, "FELDCORE", "Notifications from this server are working."); err != nil {
		return &OpError{"Could not reach ntfy: " + err.Error()}
	}
	return nil
}

func publishNtfy(ctx context.Context, base, topic, token, title, body string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	endpoint := base + "/" + url.PathEscape(topic)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Content-Type", "text/plain")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &OpError{res.Status}
	}
	return nil
}

func (s *Service) applyBackup(req Request) error {
	kind := strings.TrimSpace(req.BackupKind)
	if kind == "" || kind == "none" {
		return nil
	}
	if kind != "proton" {
		return &InputError{"Choose Proton Drive, or skip backups."}
	}
	if s.drive == nil {
		return &OpError{"Backups are not available on this server."}
	}
	schedule := strings.TrimSpace(req.BackupSchedule)
	return s.drive.Save(backup.Settings{
		Enabled: true, RemotePath: "feldcore", Schedule: schedule, Retention: 7,
		IncludeAppdata: true, Username: req.BackupUser, Password: req.BackupPass,
	})
}
