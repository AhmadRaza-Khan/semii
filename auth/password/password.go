// Package password hashes and verifies passwords with argon2id, encoded as
// PHC strings ($argon2id$v=19$m=65536,t=1,p=4$salt$hash), and checks them
// against the auth password policy.
//
// common.txt is the "10k-most-common" list from SecLists
// (https://github.com/danielmiessler/SecLists), MIT License,
// Copyright (c) 2018 Daniel Miessler.
package password

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams is the RFC 9106 second recommended option: 64 MiB, one
// pass, four lanes.
var DefaultParams = Params{Memory: 64 * 1024, Iterations: 1, Parallelism: 4, SaltLength: 16, KeyLength: 32}

// Policy bounds.
const (
	MinLength = 8
	MaxLength = 128
)

// ErrInvalidHash reports an encoded string that is not a supported PHC
// argon2id hash.
var ErrInvalidHash = errors.New("password: invalid hash")

var b64 = base64.RawStdEncoding

// Hash derives an argon2id hash of pw with a random salt.
func Hash(pw string, p Params) (string, error) {
	if p.SaltLength == 0 || p.KeyLength == 0 || p.Iterations == 0 || p.Parallelism == 0 || p.Memory == 0 {
		return "", errors.New("password: invalid params")
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches encoded, comparing in constant time.
// needsRehash is true when encoded was made with params other than
// DefaultParams, so callers can upgrade it after a successful login.
func Verify(pw, encoded string) (ok, needsRehash bool, err error) {
	return verify(pw, encoded, DefaultParams)
}

func verify(pw, encoded string, want Params) (ok, needsRehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(pw), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(key)))
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, p != want, nil
}

func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrInvalidHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	p.SaltLength, p.KeyLength = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}

//go:embed common.txt
var commonList string

// common is the lowercased common-password set, built on first use.
var common = sync.OnceValue(func() map[string]struct{} {
	set := make(map[string]struct{}, 10000)
	sc := bufio.NewScanner(strings.NewReader(commonList))
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			set[strings.ToLower(w)] = struct{}{}
		}
	}
	return set
})

// PolicyError explains why a password was rejected.
type PolicyError struct{ Reason string }

func (e *PolicyError) Error() string { return "password " + e.Reason }

// Check enforces the policy: 8 to 128 characters, no composition rules,
// and (when checkCommon) not one of the 10,000 most common passwords.
func Check(pw string, checkCommon bool) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinLength:
		return &PolicyError{fmt.Sprintf("must be at least %d characters", MinLength)}
	case n > MaxLength:
		return &PolicyError{fmt.Sprintf("must be at most %d characters", MaxLength)}
	}
	if checkCommon {
		if _, ok := common()[strings.ToLower(pw)]; ok {
			return &PolicyError{"is too common; choose a less guessable one"}
		}
	}
	return nil
}

// Hasher hashes and verifies through a semaphore so concurrent logins
// cannot exhaust memory (each argon2id call allocates Params.Memory).
type Hasher struct {
	params Params
	sem    chan struct{}
	dummy  string
}

// NewHasher returns a Hasher allowing concurrency simultaneous hashes
// (default 4 when <= 0).
func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if concurrency <= 0 {
		concurrency = 4
	}
	dummy, err := Hash("semii-dummy-password", p)
	if err != nil {
		return nil, err
	}
	return &Hasher{params: p, sem: make(chan struct{}, concurrency), dummy: dummy}, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.sem }

// Hash hashes pw with the Hasher's params.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return Hash(pw, h.params)
}

// Verify checks pw against encoded; needsRehash reports params that differ
// from the Hasher's.
func (h *Hasher) Verify(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error) {
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	return verify(pw, encoded, h.params)
}

// VerifyDummy spends the same time as a real verification and always
// fails. Call it when the account does not exist, so response timing does
// not reveal which emails are registered.
func (h *Hasher) VerifyDummy(ctx context.Context, pw string) {
	_, _, _ = h.Verify(ctx, pw, h.dummy)
}
