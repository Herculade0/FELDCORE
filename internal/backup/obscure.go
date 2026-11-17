// Package backup stores versioned restic repositories and, from the older
// Settings card, uploads one archive to Proton Drive with rclone.
// The Proton backend is unofficial.
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// cryptKey is rclone's well-known obfuscation key. It is not encryption.
// Real secrets stay in FELDCORE's secrets box; this only matches the format
// rclone expects in a config file.
var cryptKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

// Obscure matches rclone's `rclone obscure` so a generated config can be read
// by the protondrive backend.
func Obscure(plain string) (string, error) {
	block, err := aes.NewCipher(cryptKey)
	if err != nil {
		return "", err
	}
	buf := make([]byte, aes.BlockSize+len(plain))
	if _, err := io.ReadFull(rand.Reader, buf[:aes.BlockSize]); err != nil {
		return "", err
	}
	stream := cipher.NewCTR(block, buf[:aes.BlockSize])
	stream.XORKeyStream(buf[aes.BlockSize:], []byte(plain))
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Reveal reverses Obscure. Tests use it to prove a config does not store the
// plaintext password.
func Reveal(obscured string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(obscured)
	if err != nil {
		return "", err
	}
	if len(raw) < aes.BlockSize {
		return "", errors.New("obscured value is too short")
	}
	block, err := aes.NewCipher(cryptKey)
	if err != nil {
		return "", err
	}
	stream := cipher.NewCTR(block, raw[:aes.BlockSize])
	out := make([]byte, len(raw)-aes.BlockSize)
	stream.XORKeyStream(out, raw[aes.BlockSize:])
	return string(out), nil
}

func mustObscure(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	o, err := Obscure(plain)
	if err != nil {
		return "", fmt.Errorf("could not prepare the Proton Drive password")
	}
	return o, nil
}
