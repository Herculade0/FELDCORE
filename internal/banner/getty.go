package banner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// errNoLoginPrompt means a getty we still need to redraw is not at the
// prompt yet. The watcher tries again. A session that is already logged
// in is not an error: the next login reads /etc/issue itself.
var errNoLoginPrompt = errors.New("login prompt is not up yet")

type gettyStatus int

const (
	gettyAbsent gettyStatus = iota
	gettyPrompt
	gettyBusy
)

// gettyUnits are the local prompts that show /etc/issue. tty1 is the
// screen (and noVNC). ttyS0 is the serial console.
var gettyUnits = []string{
	"getty@tty1.service",
	"serial-getty@ttyS0.service",
}

// lookGetty and kickGetty are replaced by tests.
var (
	lookGetty = realLookGetty
	kickGetty = realKickGetty
)

func realLookGetty(ctx context.Context, unit string) gettyStatus {
	cmd := exec.CommandContext(ctx, "systemctl", "show", "-p", "MainPID", "--value", unit)
	out, err := cmd.Output()
	if err != nil {
		return gettyAbsent
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return gettyAbsent
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return gettyAbsent
	}
	if strings.TrimSpace(string(comm)) == "agetty" {
		return gettyPrompt
	}
	return gettyBusy
}

func realKickGetty(ctx context.Context, unit string) error {
	return exec.CommandContext(ctx, "systemctl", "restart", unit).Run()
}

// NewRefresher restarts gettys that are still sitting at the login prompt.
// next starts another redraw, which is what happens when the banner text
// changes (an address arrives, or the setup code is consumed). refresh
// retries the current redraw: a unit already restarted for it is not
// restarted again, and a logged-in session is left alone. A getty that
// has not started yet makes refresh return errNoLoginPrompt.
func NewRefresher() (next func(), refresh func(context.Context) error) {
	gen := 0
	kicked := map[string]int{}
	next = func() { gen++ }
	refresh = func(ctx context.Context) error {
		if gen == 0 {
			gen = 1
		}
		pending := false
		for _, unit := range gettyUnits {
			if kicked[unit] == gen {
				continue
			}
			switch lookGetty(ctx, unit) {
			case gettyPrompt:
				if err := kickGetty(ctx, unit); err != nil {
					return err
				}
				kicked[unit] = gen
			case gettyBusy:
				kicked[unit] = gen
			default:
				pending = true
			}
		}
		if pending {
			return errNoLoginPrompt
		}
		return nil
	}
	return next, refresh
}
