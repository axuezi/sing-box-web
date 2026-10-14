// Package auth implements password hashing, login throttling and tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: the OWASP Password Storage Cheat Sheet baseline
// (19 MiB, 2 passes, 1 lane), chosen to keep a login cheap on small VPSes.
// Parameters are stored in each hash, so they can be raised later without
// invalidating existing passwords.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// MinPasswordLen is the shortest password HashPassword accepts.
const MinPasswordLen = 10

var b64 = base64.RawStdEncoding

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	releaseMemory()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("malformed password hash")

// VerifyPassword reports whether password matches a hash from HashPassword.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var mem uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, errBadHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	releaseMemory()
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// RandomToken returns n random bytes, URL-safe base64 encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a session token, the form stored in
// the database so a leaked database does not leak live sessions.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// releaseMemory hands argon2's large, short-lived buffer back to the OS
// right away; logins are rare, so the extra GC cycle is cheap and keeps the
// resident size of an idle panel small.
func releaseMemory() { debug.FreeOSMemory() }
