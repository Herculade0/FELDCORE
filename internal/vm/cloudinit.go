package vm

import (
	"fmt"
	"strings"
)

// cloudInitISO builds a NoCloud seed with label cidata.
func cloudInitISO(spec Spec) ([]byte, error) {
	if !spec.CloudInit.Enabled {
		return nil, invalid("Cloud-init is not enabled.")
	}
	if spec.CloudInit.Password == "" && spec.CloudInit.SSHKey == "" {
		return nil, invalid("Cloud-init needs a password or an SSH public key, otherwise the new user cannot sign in.")
	}
	meta := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", spec.Name, spec.Name)
	return buildISO("cidata", []isoFile{
		{Name: "user-data", Data: []byte(userData(spec))},
		{Name: "meta-data", Data: []byte(meta)},
	})
}

func userData(spec Spec) string {
	c := spec.CloudInit
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "hostname: %s\n", spec.Name)
	b.WriteString("users:\n")
	fmt.Fprintf(&b, "  - name: %s\n", c.User)
	b.WriteString("    sudo: ALL=(ALL) NOPASSWD:ALL\n")
	b.WriteString("    groups: [sudo]\n")
	b.WriteString("    shell: /bin/bash\n")
	if c.Password == "" {
		b.WriteString("    lock_passwd: true\n")
	} else {
		b.WriteString("    lock_passwd: false\n")
	}
	if c.SSHKey != "" {
		b.WriteString("    ssh_authorized_keys:\n")
		fmt.Fprintf(&b, "      - %s\n", c.SSHKey)
	}
	if c.Password != "" {
		b.WriteString("ssh_pwauth: true\n")
		b.WriteString("chpasswd:\n")
		b.WriteString("  expire: false\n")
		b.WriteString("  users:\n")
		b.WriteString("    - name: " + c.User + "\n")
		b.WriteString("      type: text\n")
		b.WriteString("      password: " + yamlQuote(c.Password) + "\n")
	}
	return b.String()
}

func yamlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
