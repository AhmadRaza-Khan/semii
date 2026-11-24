package password

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cheap keeps tests fast; DefaultParams is exercised once.
var cheap = Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashVerify(t *testing.T) {
	enc, err := Hash("correct horse battery", DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Errorf("PHC format: %s", enc)
	}
	ok, rehash, err := Verify("correct horse battery", enc)
	if !ok || rehash || err != nil {
		t.Errorf("verify: %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := Verify("wrong", enc); ok {
		t.Error("wrong password accepted")
	}
	other, _ := Hash("correct horse battery", DefaultParams)
	if other == enc {
		t.Error("salt not random")
	}

	old, _ := Hash("pw-with-old-params", cheap)
	ok, rehash, err = Verify("pw-with-old-params", old)
	if !ok || !rehash || err != nil {
		t.Errorf("old params should need rehash: %v %v %v", ok, rehash, err)
	}
}

func TestInvalidHashes(t *testing.T) {
	good, _ := Hash("x", cheap)
	parts := strings.Split(good, "$")
	bad := []string{
		"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=1,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=x$c2FsdA$a2V5",
		"$argon2id$v=19$m=0,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$" + parts[3] + "$!!!$" + parts[5],
		"$argon2id$v=19$" + parts[3] + "$" + parts[4] + "$!!!",
		"$argon2id$v=19$" + parts[3] + "$$" + parts[5],
	}
	for _, b := range bad {
		if _, _, err := Verify("x", b); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%q: %v", b, err)
		}
	}
	if _, err := Hash("x", Params{}); err == nil {
		t.Error("zero params accepted")
	}
}

func TestCheck(t *testing.T) {
	cases := map[string]bool{
		"short":                     false,
		"password":                  false, // common
		"PassWord":                  false, // common, case-insensitive
		"qwertyuiop":                false,
		strings.Repeat("a", 129):    false,
		"tr0ub4dor-staple":          true,
		"ünïcödé-pässwörd":          true,
		strings.Repeat("é", 128):    true, // counted in characters, not bytes
		"no composition rules here": true,
	}
	for pw, want := range cases {
		err := Check(pw, true)
		if (err == nil) != want {
			t.Errorf("%q: %v", pw, err)
		}
		var pe *PolicyError
		if err != nil && !errors.As(err, &pe) {
			t.Errorf("%q: not a PolicyError", pw)
		}
	}
	if err := Check("password", false); err != nil {
		t.Errorf("common check disabled: %v", err)
	}
	if len(common()) < 9000 {
		t.Errorf("common list has %d entries", len(common()))
	}
}

func TestHasherConcurrencyLimit(t *testing.T) {
	h, err := NewHasher(cheap, 2)
	if err != nil {
		t.Fatal(err)
	}
	var cur, peak atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.acquire(context.Background())
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			cur.Add(-1)
			h.release()
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Errorf("peak concurrency %d > 2", peak.Load())
	}

	enc, err := h.Hash(context.Background(), "hunter2-but-longer")
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := h.Verify(context.Background(), "hunter2-but-longer", enc); !ok || rehash || err != nil {
		t.Errorf("%v %v %v", ok, rehash, err)
	}
	h.VerifyDummy(context.Background(), "anything")

	// A full semaphore respects context cancellation.
	full, _ := NewHasher(cheap, 1)
	_ = full.acquire(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := full.Hash(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("hash: %v", err)
	}
	if _, _, err := full.Verify(ctx, "x", enc); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("verify: %v", err)
	}
	if def, _ := NewHasher(cheap, 0); cap(def.sem) != 4 {
		t.Error("default concurrency")
	}
	if _, err := NewHasher(Params{}, 1); err == nil {
		t.Error("bad params accepted")
	}
}
