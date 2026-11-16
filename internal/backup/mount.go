package backup

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

type osCmd struct{}

func (osCmd) Look(name string) (string, error) { return exec.LookPath(name) }

func (osCmd) Run(ctx context.Context, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, op(clipErr(text))
		}
		return "", err
	}
	return text, nil
}

type osMounter struct{}

func (osMounter) Mount(ctx context.Context, spec MountSpec) error {
	if err := os.MkdirAll(spec.Mountpoint, 0o700); err != nil {
		return op("Could not prepare the folder where the backup share is attached.")
	}
	switch spec.Kind {
	case "smb":
		return mountSMB(ctx, spec)
	case "nfs":
		return mountNFS(ctx, spec)
	default:
		return input("That destination cannot be attached.")
	}
}

func (osMounter) Unmount(ctx context.Context, mountpoint string) error {
	if strings.TrimSpace(mountpoint) == "" {
		return nil
	}
	if err := exec.CommandContext(ctx, "umount", mountpoint).Run(); err != nil {
		_ = exec.CommandContext(ctx, "umount", "-l", mountpoint).Run()
	}
	return nil
}

func mountSMB(ctx context.Context, spec MountSpec) error {
	if _, err := exec.LookPath("mount.cifs"); err != nil {
		if _, err := exec.LookPath("mount"); err != nil {
			return input("Install cifs-utils so FELDCORE can attach an SMB backup.")
		}
	}
	cred := spec.Mountpoint + ".cred"
	body := "username=" + spec.Username + "\npassword=" + spec.Password + "\n"
	if spec.Username == "" {
		body = "username=guest\npassword=\n"
	}
	if err := os.WriteFile(cred, []byte(body), 0o600); err != nil {
		return op("Could not prepare the SMB credentials.")
	}
	defer os.Remove(cred)
	args := smbMountArgs(spec, cred)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		msg = strings.ReplaceAll(msg, spec.Password, "[redacted]")
		if msg == "" {
			msg = "the share did not attach"
		}
		return op("Could not attach the SMB share: " + clipErr(msg))
	}
	return nil
}

func mountNFS(ctx context.Context, spec MountSpec) error {
	if _, err := exec.LookPath("mount"); err != nil {
		return input("Install NFS client tools so FELDCORE can attach an NFS backup.")
	}
	args := nfsMountArgs(spec)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "the export did not attach"
		}
		return op("Could not attach the NFS export: " + clipErr(msg))
	}
	return nil
}

func smbMountArgs(spec MountSpec, cred string) []string {
	opts := "credentials=" + cred + ",uid=0,gid=0,file_mode=0600,dir_mode=0700,noserverino"
	if spec.Port > 0 && spec.Port != 445 {
		opts += ",port=" + itoa(spec.Port)
	}
	return []string{"mount", "-t", "cifs", spec.Source, spec.Mountpoint, "-o", opts}
}

func nfsMountArgs(spec MountSpec) []string {
	opts := "nfsvers=4.1,soft,timeo=10"
	if spec.Port > 0 && spec.Port != 2049 {
		opts += ",port=" + itoa(spec.Port)
	}
	return []string{"mount", "-t", "nfs", "-o", opts, spec.Source, spec.Mountpoint}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func clipErr(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
