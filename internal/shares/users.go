package shares

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// Share users are the accounts people type into Windows/macOS to open a
// private share. Samba needs a matching unix account, so FELDCORE creates a
// locked-down one (no home, no shell) and tracks it in its state. It only
// ever modifies or deletes accounts it created.

var shareUserRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)

func validSharePassword(pw string) error {
	if len(pw) < 8 {
		return invalid("Use a password of at least 8 characters.")
	}
	if len(pw) > 128 {
		return invalid("Use a password of at most 128 characters.")
	}
	for _, r := range pw {
		if r < 0x20 || r == 0x7f {
			return invalid("Passwords cannot contain control characters.")
		}
	}
	return nil
}

func (m *Manager) requireSamba() error {
	if _, ok := findBin("smbpasswd"); !ok {
		return &OpError{"Install Samba before adding share users."}
	}
	if os.Geteuid() != 0 {
		return &OpError{"FELDCORE must run as root to manage share users."}
	}
	return nil
}

func (m *Manager) owns(name string) bool {
	for _, u := range m.st.Snapshot().ShareUsers {
		if u == name {
			return true
		}
	}
	return false
}

// setSambaPassword sets (and with add, creates) the Samba password entry.
// The password travels on stdin, never on the command line.
func setSambaPassword(ctx context.Context, name, password string, add bool) error {
	args := []string{"-s"}
	if add {
		args = append(args, "-a")
	}
	args = append(args, name)
	_, err := run(ctx, 30*time.Second, password+"\n"+password+"\n", "smbpasswd", args...)
	return err
}

// AddUser creates a share user.
func (m *Manager) AddUser(ctx context.Context, name, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !shareUserRe.MatchString(name) {
		return invalid("User names start with a lowercase letter and use lowercase letters, numbers, hyphens and underscores (up to 31 characters).")
	}
	if err := validSharePassword(password); err != nil {
		return err
	}
	if m.owns(name) {
		return invalid("A share user named %q already exists.", name)
	}
	if _, err := user.Lookup(name); err == nil {
		return invalid("%q is already a system account on this server. Choose another name.", name)
	}
	if err := m.requireSamba(); err != nil {
		return err
	}

	ctx = context.WithoutCancel(ctx)
	if _, err := run(ctx, 30*time.Second, "", "useradd",
		"--no-create-home", "--no-user-group", "--gid", m.id.Group,
		"--shell", "/usr/sbin/nologin", "--comment", "FELDCORE share user", name); err != nil {
		return &OpError{fmt.Sprintf("Could not create the account: %v", err)}
	}
	if err := setSambaPassword(ctx, name, password, true); err != nil {
		_, _ = run(ctx, 30*time.Second, "", "userdel", name) // roll back
		return &OpError{fmt.Sprintf("Could not set the Samba password: %v", err)}
	}
	if err := m.st.Update(func(s *store.State) error {
		s.ShareUsers = append(s.ShareUsers, name)
		sort.Strings(s.ShareUsers)
		return nil
	}); err != nil {
		return err
	}
	m.log.Info("share user created", "user", name)
	return nil
}

// SetUserPassword changes a share user's password.
func (m *Manager) SetUserPassword(ctx context.Context, name, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.owns(name) {
		return ErrNotFound
	}
	if err := validSharePassword(password); err != nil {
		return err
	}
	if err := m.requireSamba(); err != nil {
		return err
	}
	if err := setSambaPassword(context.WithoutCancel(ctx), name, password, false); err != nil {
		return &OpError{fmt.Sprintf("Could not change the password: %v", err)}
	}
	m.log.Info("share user password changed", "user", name)
	return nil
}

// DeleteUser removes a share user that no share still relies on.
func (m *Manager) DeleteUser(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.owns(name) {
		return ErrNotFound
	}
	var usedBy []string
	for _, sh := range m.st.Snapshot().Shares {
		for _, u := range sh.SMB.Users {
			if u == name {
				usedBy = append(usedBy, sh.Name)
			}
		}
	}
	if len(usedBy) > 0 {
		return invalid("%s still has access to: %s. Remove them from those shares first.",
			name, strings.Join(usedBy, ", "))
	}
	if os.Geteuid() != 0 {
		return &OpError{"FELDCORE must run as root to manage share users."}
	}

	ctx = context.WithoutCancel(ctx)
	// Best effort: the Samba entry may already be gone.
	_, _ = run(ctx, 30*time.Second, "", "smbpasswd", "-x", name)
	if _, err := user.Lookup(name); err == nil {
		if _, err := run(ctx, 30*time.Second, "", "userdel", name); err != nil {
			return &OpError{fmt.Sprintf("Could not remove the account: %v", err)}
		}
	}
	if err := m.st.Update(func(s *store.State) error {
		kept := s.ShareUsers[:0]
		for _, u := range s.ShareUsers {
			if u != name {
				kept = append(kept, u)
			}
		}
		s.ShareUsers = kept
		return nil
	}); err != nil {
		return err
	}
	m.log.Info("share user deleted", "user", name)
	return nil
}
