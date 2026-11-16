package citizenid

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argonParams are the argon2id settings for Citizen ID passwords.
// The administrator account stays on bcrypt so that sign-in is unchanged.
type argonParams struct {
	time    uint32
	memory  uint32
	threads uint8
	keyLen  uint32
}

// prodArgon follows the RFC suggestion of one pass over 64 MiB.
var prodArgon = argonParams{time: 1, memory: 64 * 1024, threads: 2, keyLen: 32}

func hashPassword(p argonParams, password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	var sum []byte
	limitArgon(func() {
		sum = argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	})
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, time uint32
	var threads uint8
	for _, f := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return false
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return false
		}
		switch k {
		case "m":
			memory = uint32(n)
		case "t":
			time = uint32(n)
		case "p":
			threads = uint8(n)
		default:
			return false
		}
	}
	if memory == 0 || time == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	var sum []byte
	limitArgon(func() {
		sum = argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	})
	return subtle.ConstantTimeCompare(sum, want) == 1
}

// argonSlots caps concurrent argon2id hashes. Each one can use tens of
// megabytes, so an open sign-in form must not be able to start hundreds.
var argonSlots = make(chan struct{}, 4)

func limitArgon(fn func()) {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	fn()
}
