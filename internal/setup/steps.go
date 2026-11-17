package setup

import "github.com/Herculade0/feldcore/internal/store"

// Steps are the first-run pages, in order.
var Steps = []string{"welcome", "host", "network", "admin", "storage", "remote", "notify", "backup", "summary"}

// RerunSteps are the settings replay. Formatting a disk and creating the
// administrator are not included.
var RerunSteps = []string{"host", "network", "signin", "remote", "notify", "backup", "summary"}

func optional(step string) bool {
	switch step {
	case "host", "network", "storage", "remote", "notify", "backup", "signin":
		return true
	default:
		return false
	}
}

func stepList(w store.Wizard) []string {
	if w.Rerun {
		return RerunSteps
	}
	return Steps
}

func cursor(w store.Wizard) string {
	if w.Rerun {
		if w.RerunStep == "" {
			return "host"
		}
		return w.RerunStep
	}
	if w.Step == "" {
		return "welcome"
	}
	return w.Step
}

func canonical(step string, rerun bool) string {
	if rerun {
		switch step {
		case "host", "network", "signin", "remote", "notify", "backup", "summary":
			return step
		default:
			return "host"
		}
	}
	switch step {
	case "welcome", "host", "network", "admin", "storage", "remote", "notify", "backup", "summary":
		return step
	case "packages", "shares", "security", "updates":
		// Older wizards stopped on these pages. Resume at remote access.
		return "remote"
	case "":
		return "welcome"
	default:
		return "welcome"
	}
}

func needsRoot(step string, req Request) bool {
	if req.Skip || req.Back || req.Check || req.Action != "" || req.BeginRerun || req.DiscardRerun {
		return false
	}
	switch step {
	case "host", "network":
		return true
	case "storage":
		return req.StorageFormat
	default:
		return false
	}
}

func advance(w *store.Wizard) {
	list := stepList(*w)
	cur := cursor(*w)
	next := cur
	for i, step := range list {
		if step == cur && i+1 < len(list) {
			next = list[i+1]
			break
		}
	}
	setCursor(w, next)
}

func retreat(w *store.Wizard) {
	list := stepList(*w)
	cur := cursor(*w)
	prev := cur
	for i, step := range list {
		if step == cur && i > 0 {
			prev = list[i-1]
			break
		}
	}
	setCursor(w, prev)
}

func setCursor(w *store.Wizard, step string) {
	if w.Rerun {
		w.RerunStep = step
		return
	}
	w.Step = step
}

func rememberSkip(w *store.Wizard, step string) {
	for _, s := range w.Skipped {
		if s == step {
			return
		}
	}
	w.Skipped = append(w.Skipped, step)
}
