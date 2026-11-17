package auth

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckPasswordIsRateLimited(t *testing.T) {
	a := newTestService(t)
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := a.CheckPassword("192.0.2.1", "nope"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := a.CheckPassword("192.0.2.1", testPass); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("password check was not rate limited: %v", err)
	}
	if err := a.CheckPassword("192.0.2.2", testPass); err != nil {
		t.Fatalf("another address: %v", err)
	}
}

func TestLoginSuccessDoesNotResetTheAddress(t *testing.T) {
	a := newTestService(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, _, err := a.Login("attacker", "admin", "guess"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := a.Login("attacker", "admin", testPass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login("attacker", "admin", "guess"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("the success cleared the address bucket: %v", err)
	}
	if _, _, err := a.Login("attacker", "admin", testPass); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("after the kept failures: %v", err)
	}
}

func TestLoginLimiterIsAtomic(t *testing.T) {
	lim := newLimiter(8, time.Minute)
	var passed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, ok := lim.begin(time.Now(), "ip:203.0.113.1")
			if !ok {
				return
			}
			passed.Add(1)
			done(true)
		}()
	}
	wg.Wait()
	if got := passed.Load(); got > 8 {
		t.Fatalf("lockout let %d attempts through", got)
	}
}

func TestLoginRejectsOverlongPassword(t *testing.T) {
	a := newTestService(t)
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login("ip", "admin", strings.Repeat("a", 80)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("over-long password: %v", err)
	}
}
