package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// AppNetworkPrefix is the bridge Docker network created for one app.
// The full name is the prefix plus the container name, for example
// feldcore-app-plex. Containers on that network cannot reach containers
// on other networks unless they are linked.
const AppNetworkPrefix = "feldcore-app-"

// Isolation is how a container sits relative to other apps.
const (
	// IsolatePrivate gives the container its own bridge. This is the default
	// when FELDCORE creates an app.
	IsolatePrivate = "private"
	// IsolateShared keeps the network named on the spec, usually the Docker
	// bridge. Existing containers stay shared until someone isolates them.
	IsolateShared = "shared"
)

const (
	netLabel   = "feldcore.network" // "private" when FELDCORE assigned the app network
	linksLabel = "feldcore.links"   // comma-separated container names
)

// AppNetwork is the private bridge name for a container.
func AppNetwork(name string) string {
	return AppNetworkPrefix + name
}

func isAppNetwork(network, name string) bool {
	return name != "" && network == AppNetwork(name)
}

// netPlan is the network change for one create or edit. It is pure: nothing
// here talks to Docker.
type netPlan struct {
	Spec        Spec
	Ensure      string // bridge to create before the container
	LinkNetwork string // network the linked containers join
	ConnectSelf bool   // this container joins LinkNetwork in addition to its primary network
	Retire      string // previous app network to detach and delete when it is empty
}

// planNetwork decides the network for s.
//
// creating is true for a new container. An empty Isolate then means private
// when the container would otherwise land on the shared bridge. On an edit,
// an empty Isolate keeps whatever the container already has, so existing
// containers are not moved.
//
// prevName, prevMode and prevLinks describe the container being replaced.
// They are empty when creating.
//
// VPN-routed containers and gateway containers are left alone. Docker will
// not attach a second network to a container whose network mode is
// container:<gateway>, and that mode is what the kill switch depends on.
func planNetwork(s Spec, creating bool, prevName, prevMode string, prevLinks []string) (netPlan, error) {
	s.Links = tidyLinks(s.Links)
	switch s.Isolate {
	case "", IsolatePrivate, IsolateShared:
	default:
		return netPlan{}, invalid("Choose a private network or the shared one.")
	}

	if s.Gateway || s.VPN != "" || strings.HasPrefix(strings.TrimSpace(s.Network), "container:") {
		s.Isolate = IsolateShared
		s.Links = nil
		return netPlan{Spec: s, Retire: retireNetwork(prevName, prevMode, prevLinks)}, nil
	}

	mode := strings.TrimSpace(s.Network)
	if mode == "" || mode == "default" {
		mode = "bridge"
	}

	if mode == "host" || mode == "none" {
		if len(s.Links) > 0 {
			return netPlan{}, invalid("Containers using the host or none network cannot be linked to other containers.")
		}
		s.Isolate = IsolateShared
		s.Network = mode
		return netPlan{Spec: s, Retire: retireNetwork(prevName, prevMode, prevLinks)}, nil
	}

	if err := validateLinkNames(s.Name, s.Links); err != nil {
		return netPlan{}, err
	}

	private := s.Isolate == IsolatePrivate
	if s.Isolate == "" {
		if creating {
			private = mode == "bridge" || isAppNetwork(mode, s.Name)
		} else if isAppNetwork(prevMode, prevName) && (mode == "bridge" || isAppNetwork(mode, s.Name) || isAppNetwork(mode, prevName)) {
			private = true
		}
	}

	if private {
		if s.Name == "" {
			return netPlan{}, invalid("Name this container. Its private network is named after it.")
		}
		netName := AppNetwork(s.Name)
		s.Isolate = IsolatePrivate
		s.Network = netName
		p := netPlan{Spec: s, Ensure: netName, LinkNetwork: netName}
		if prevName != "" && prevName != s.Name && retireNetwork(prevName, prevMode, prevLinks) != "" {
			p.Retire = AppNetwork(prevName)
		}
		return p, nil
	}

	s.Isolate = IsolateShared
	if isAppNetwork(mode, s.Name) || (prevName != "" && isAppNetwork(mode, prevName)) {
		mode = "bridge"
	}
	s.Network = mode
	p := netPlan{Spec: s, Retire: retireNetwork(prevName, prevMode, prevLinks)}
	if len(s.Links) > 0 {
		if s.Name == "" {
			return netPlan{}, invalid("Name this container before linking other apps to it.")
		}
		p.LinkNetwork = AppNetwork(s.Name)
		p.Ensure = p.LinkNetwork
		p.ConnectSelf = true
		if p.Retire == p.LinkNetwork {
			p.Retire = ""
		}
	}
	return p, nil
}

func retireNetwork(prevName, prevMode string, prevLinks []string) string {
	if prevName == "" {
		return ""
	}
	if isAppNetwork(prevMode, prevName) || len(prevLinks) > 0 {
		return AppNetwork(prevName)
	}
	return ""
}

func tidyLinks(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func splitLinks(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return tidyLinks(strings.Split(raw, ","))
}

func validateLinkNames(self string, links []string) error {
	if len(links) > 32 {
		return invalid("Link at most 32 containers.")
	}
	for _, n := range links {
		if !nameRe.MatchString(n) {
			return invalid("Linked container names use letters, numbers and _ . - and start with a letter or number.")
		}
		if self != "" && n == self {
			return invalid("A container cannot be linked to itself.")
		}
	}
	return nil
}

// prepareNetwork checks the plan and creates the app bridge when one is needed.
// The container itself is not changed here.
func (m *Manager) prepareNetwork(ctx context.Context, s Spec, creating bool, prevName, prevMode string, prevLinks []string) (netPlan, error) {
	plan, err := planNetwork(s, creating, prevName, prevMode, prevLinks)
	if err != nil {
		return netPlan{}, err
	}
	if err := m.vetLinks(ctx, plan.Spec.Links); err != nil {
		return netPlan{}, err
	}
	ensure := plan.Ensure
	if ensure == "" && strings.HasPrefix(plan.Spec.Network, AppNetworkPrefix) {
		ensure = plan.Spec.Network
	}
	if ensure != "" {
		if err := m.ensureAppNetwork(ctx, ensure); err != nil {
			return netPlan{}, &OpError{fmt.Sprintf("Could not create the network %s: %v", ensure, err)}
		}
	}
	return plan, nil
}

// vetLinks reports containers that cannot join a shared bridge.
func (m *Manager) vetLinks(ctx context.Context, links []string) error {
	for _, name := range links {
		in, err := m.inspect(ctx, name)
		if NotFound(err) {
			return invalid("There is no container named %s to link.", name)
		}
		if err != nil {
			return err
		}
		mode := strOf(in.HostConfig["NetworkMode"])
		if mode == "" || mode == "default" {
			mode = "bridge"
		}
		switch {
		case mode == "host":
			return invalid("%s uses the host network, so it cannot join a shared network.", name)
		case mode == "none":
			return invalid("%s has no network, so it cannot be linked.", name)
		case strings.HasPrefix(mode, "container:"):
			return invalid("%s is routed through another container, so it cannot join a shared network.", name)
		}
	}
	return nil
}

// ensureAppNetwork creates a FELDCORE app bridge if it does not already exist.
func (m *Manager) ensureAppNetwork(ctx context.Context, name string) error {
	if !strings.HasPrefix(name, AppNetworkPrefix) {
		return invalid("%q is not an app network.", name)
	}
	err := m.c.postJSON(ctx, "/networks/create", nil, map[string]any{
		"Name":           name,
		"Driver":         "bridge",
		"CheckDuplicate": true,
		"Labels": map[string]string{
			ManagedLabel: "true",
			RoleLabel:    "appnet",
		},
	}, nil)
	if err == nil || alreadyExists(err) {
		return nil
	}
	return err
}

// DisconnectNetwork detaches a container. A missing network or a container
// that is not attached is not an error.
func (m *Manager) DisconnectNetwork(ctx context.Context, network, container string) error {
	if network == "" || container == "" {
		return nil
	}
	err := m.c.postJSON(ctx, "/networks/"+esc(network)+"/disconnect", nil, map[string]any{
		"Container": container,
		"Force":     true,
	}, nil)
	if err == nil || NotFound(err) {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not connected") || strings.Contains(msg, "is not attached") {
		return nil
	}
	return err
}

// applyLinks connects and disconnects the containers named by the plan.
// self is the container that was just created.
func (m *Manager) applyLinks(ctx context.Context, self string, plan netPlan, prev []string) error {
	if plan.LinkNetwork != "" && plan.ConnectSelf && self != "" {
		if err := m.ConnectNetwork(ctx, plan.LinkNetwork, self); err != nil {
			return &OpError{fmt.Sprintf("The container was saved, but it could not join %s: %v", plan.LinkNetwork, err)}
		}
	}
	want := map[string]bool{}
	for _, name := range plan.Spec.Links {
		want[name] = true
		if plan.LinkNetwork == "" {
			continue
		}
		if err := m.ConnectNetwork(ctx, plan.LinkNetwork, name); err != nil {
			return &OpError{fmt.Sprintf("The container was saved, but %s could not be linked: %v", name, err)}
		}
	}
	for _, name := range prev {
		if !want[name] && plan.LinkNetwork != "" {
			_ = m.DisconnectNetwork(ctx, plan.LinkNetwork, name)
		}
		if plan.Retire != "" {
			_ = m.DisconnectNetwork(ctx, plan.Retire, name)
		}
	}
	if plan.Retire != "" {
		if self != "" {
			_ = m.DisconnectNetwork(ctx, plan.Retire, self)
		}
		m.removeAppNetwork(ctx, plan.Retire)
	}
	return nil
}

// removeAppNetwork deletes an empty app bridge. Networks FELDCORE does not
// own, and networks something is still using, are left in place.
func (m *Manager) removeAppNetwork(ctx context.Context, name string) {
	if !strings.HasPrefix(name, AppNetworkPrefix) {
		return
	}
	_ = m.c.do2(ctx, "DELETE", "/networks/"+esc(name), nil)
}
