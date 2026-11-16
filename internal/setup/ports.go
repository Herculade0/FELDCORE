package setup

import (
	"context"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/vpn"
)

// The concrete services satisfy the wizard's seams. A signature change in
// those packages fails here instead of only when the daemon is wired.
var (
	_ Auditor        = (*auth.Service)(nil)
	_ TunnelSaver    = (*remote.Service)(nil)
	_ VPNSaver       = (*vpn.Service)(nil)
	_ MailSaver      = (*notify.Service)(nil)
	_ DriveSaver     = (*backup.Service)(nil)
	_ secretBox      = (*secrets.Box)(nil)
	_ storage.Volume = (*storage.Linux)(nil)
)

// Auditor records a state change. *auth.Service satisfies it.
type Auditor interface {
	Record(tokenID string, ev store.AuditEvent) error
}

// TunnelSaver is the Cloudflare tunnel. *remote.Service satisfies it.
type TunnelSaver interface {
	Save(ctx context.Context, in remote.SaveInput) error
}

// VPNSaver is WireGuard. *vpn.Service satisfies it.
type VPNSaver interface {
	Save(ctx context.Context, in vpn.SaveRequest) (store.VPNTunnel, error)
	SetEnabled(ctx context.Context, on bool) error
}

// MailSaver is email notifications. *notify.Service satisfies it.
type MailSaver interface {
	Save(ctx context.Context, in notify.Settings) error
	Test(ctx context.Context) error
}

// DriveSaver is Proton Drive backups. *backup.Service satisfies it.
type DriveSaver interface {
	Save(in backup.Settings) error
}

// secretBox stores the ntfy token. The authenticator secret lives in auth.
type secretBox interface {
	Put(name, value string) error
	Get(name string) (string, bool, error)
}

// UseAccounts connects administrator creation and authenticator enrolment.
func (s *Service) UseAccounts(a *auth.Service) { s.accounts = a }

// UseAudit records wizard changes in the audit log.
func (s *Service) UseAudit(a Auditor) { s.auditor = a }

// UseVolume connects disk inventory. A nil volume only accepts a folder path.
func (s *Service) UseVolume(v storage.Volume) { s.disks = v }

// UseTunnel connects Cloudflare Tunnel quick setup.
func (s *Service) UseTunnel(t TunnelSaver) { s.tunnel = t }

// UseVPN connects WireGuard quick setup.
func (s *Service) UseVPN(v VPNSaver) { s.vpn = v }

// UseMail connects email notifications.
func (s *Service) UseMail(m MailSaver) { s.mail = m }

// UseDrive connects Proton Drive backups.
func (s *Service) UseDrive(d DriveSaver) { s.drive = d }

// UseSecrets stores the ntfy token outside the state file.
func (s *Service) UseSecrets(b secretBox) { s.box = b }
