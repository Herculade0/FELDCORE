package osupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/blake2b"
)

// Minisign public keys and signatures, both the legacy "Ed" form and the
// hashed "ED" form current minisign writes. The signed bytes are the file
// (legacy) or the BLAKE2b-512 of the file (hashed). A second signature covers
// the first signature concatenated with the trusted comment.

const (
	minisignPubSize  = 2 + 8 + ed25519.PublicKeySize
	minisignSigSize  = 2 + 8 + ed25519.SignatureSize
	minisignPrivSize = 2 + 8 + ed25519.PrivateKeySize
)

// PublicKey is a minisign public key.
type PublicKey struct {
	id  [8]byte
	key ed25519.PublicKey
}

// PrivateKey is the matching secret. It is never written into an image.
type PrivateKey struct {
	id   [8]byte
	priv ed25519.PrivateKey
}

// ID is the eight-byte key id minisign prints, as hex.
func (k PublicKey) ID() string { return hex.EncodeToString(k.id[:]) }

// ID is the eight-byte key id, as hex.
func (k PrivateKey) ID() string { return hex.EncodeToString(k.id[:]) }

// Public returns the public half.
func (k PrivateKey) Public() PublicKey {
	pub := make([]byte, ed25519.PublicKeySize)
	copy(pub, k.priv[32:])
	return PublicKey{id: k.id, key: pub}
}

// GenerateKey makes a new signing key. The caller stores it outside the repo.
func GenerateKey() (PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PrivateKey{}, err
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return PrivateKey{}, err
	}
	// ed25519.GenerateKey returns a 64-byte private key whose second half is pub.
	if ed25519.Verify(pub, []byte("feldcore"), ed25519.Sign(priv, []byte("feldcore"))) == false {
		return PrivateKey{}, fmt.Errorf("generated key did not round-trip")
	}
	return PrivateKey{id: id, priv: priv}, nil
}

// MarshalPublic encodes a minisign public key file.
func MarshalPublic(k PublicKey) string {
	buf := make([]byte, minisignPubSize)
	copy(buf[0:2], "Ed")
	copy(buf[2:10], k.id[:])
	copy(buf[10:], k.key)
	return "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(buf) + "\n"
}

// ParsePublic reads a minisign public key file.
func ParsePublic(text string) (PublicKey, error) {
	line := base64Line(text)
	if line == "" {
		return PublicKey{}, input("The update public key is empty.")
	}
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != minisignPubSize || string(raw[0:2]) != "Ed" {
		return PublicKey{}, input("That is not a minisign public key.")
	}
	var id [8]byte
	copy(id[:], raw[2:10])
	key := make([]byte, ed25519.PublicKeySize)
	copy(key, raw[10:])
	return PublicKey{id: id, key: key}, nil
}

// MarshalPrivate encodes the signing key. This file must not be committed.
func MarshalPrivate(k PrivateKey) string {
	buf := make([]byte, minisignPrivSize)
	copy(buf[0:2], "Ed")
	copy(buf[2:10], k.id[:])
	copy(buf[10:], k.priv)
	return "untrusted comment: FELDCORE update signing key. Never commit this file.\n" +
		base64.StdEncoding.EncodeToString(buf) + "\n"
}

// ParsePrivate reads a signing key written by MarshalPrivate.
func ParsePrivate(text string) (PrivateKey, error) {
	line := base64Line(text)
	if line == "" {
		return PrivateKey{}, fmt.Errorf("the signing key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != minisignPrivSize || string(raw[0:2]) != "Ed" {
		return PrivateKey{}, fmt.Errorf("that is not a FELDCORE update signing key")
	}
	var id [8]byte
	copy(id[:], raw[2:10])
	priv := make([]byte, ed25519.PrivateKeySize)
	copy(priv, raw[10:])
	return PrivateKey{id: id, priv: priv}, nil
}

// Sign writes a minisign signature for data. filename is only the comment.
func Sign(k PrivateKey, filename string, data []byte) ([]byte, error) {
	sum := blake2b.Sum512(data)
	sig := ed25519.Sign(k.priv, sum[:])
	body := make([]byte, minisignSigSize)
	copy(body[0:2], "ED")
	copy(body[2:10], k.id[:])
	copy(body[10:], sig)
	name := path.Base(filename)
	if name == "." || name == "/" || name == "" {
		name = "feldcore-os.json"
	}
	trusted := fmt.Sprintf("timestamp:%d\tfile:%s\thashed", time.Now().Unix(), name)
	if !commentOK(trusted) {
		return nil, fmt.Errorf("trusted comment is not printable")
	}
	globalMsg := append(append([]byte{}, sig...), trusted...)
	global := ed25519.Sign(k.priv, globalMsg)
	var b strings.Builder
	b.WriteString("untrusted comment: signature from minisign secret key\n")
	b.WriteString(base64.StdEncoding.EncodeToString(body))
	b.WriteByte('\n')
	b.WriteString("trusted comment: ")
	b.WriteString(trusted)
	b.WriteByte('\n')
	b.WriteString(base64.StdEncoding.EncodeToString(global))
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// Verify checks a minisign signature over data.
func Verify(pub PublicKey, data, sigFile []byte) error {
	untrusted, sigLine, trusted, globalLine, err := splitSignature(string(sigFile))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(untrusted, "untrusted comment: ") {
		return input("The signature file has no untrusted comment.")
	}
	raw, err := base64.StdEncoding.DecodeString(sigLine)
	if err != nil || len(raw) != minisignSigSize {
		return input("The signature is not a minisign signature.")
	}
	alg := string(raw[0:2])
	var hashed bool
	switch alg {
	case "ED":
		hashed = true
	case "Ed":
		hashed = false
	default:
		return input("The signature uses an unsupported algorithm.")
	}
	var id [8]byte
	copy(id[:], raw[2:10])
	if id != pub.id {
		return input("The signature was made by a different key.")
	}
	sig := raw[10:]
	var msg []byte
	if hashed {
		sum := blake2b.Sum512(data)
		msg = sum[:]
	} else {
		msg = data
	}
	if !ed25519.Verify(pub.key, msg, sig) {
		return input("The update signature does not match the release.")
	}
	if !strings.HasPrefix(trusted, "trusted comment: ") {
		return input("The signature has no trusted comment.")
	}
	comment := strings.TrimPrefix(trusted, "trusted comment: ")
	if !commentOK(comment) {
		return input("The signature comment is not printable.")
	}
	global, err := base64.StdEncoding.DecodeString(globalLine)
	if err != nil || len(global) != ed25519.SignatureSize {
		return input("The comment signature is not valid.")
	}
	covered := append(append([]byte{}, sig...), comment...)
	if !ed25519.Verify(pub.key, covered, global) {
		return input("The comment signature does not match.")
	}
	return nil
}

func splitSignature(text string) (untrusted, sigLine, trusted, global string, err error) {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 4 {
		return "", "", "", "", input("The signature file is incomplete.")
	}
	return lines[0], lines[1], lines[2], lines[3], nil
}

func base64Line(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		return line
	}
	return ""
}

func commentOK(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
