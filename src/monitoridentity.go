package main

import (
	"errors"
	"slices"
	"strings"
)

// Owners is present only after complete account/organization discovery.
// Viewer comes from the same API response as the rows, including pinned rows.
type monitorHostIdentity struct {
	Viewer     string
	Owners     []string
	Conflicted bool
}

func normalizedMonitorOwners(owners []string) []string {
	normalized := make([]string, 0, len(owners))
	for _, owner := range owners {
		normalized = append(normalized, strings.ToLower(owner))
	}
	slices.Sort(normalized)
	return slices.Compact(normalized)
}

func monitorOwnerViewer(owners []string) string {
	for _, owner := range owners {
		if strings.HasPrefix(owner, "user:") {
			return strings.TrimPrefix(owner, "user:")
		}
	}
	return ""
}

func bindMonitorHostIdentity(result *monitorFetchResult, request monitorHostQuery) (*monitorFetchResult, error) {
	viewer := strings.ToLower(strings.TrimSpace(result.ViewerLogin))
	identity := monitorHostIdentity{Viewer: viewer, Owners: normalizedMonitorOwners(request.OwnerScope)}
	result.HostIdentities = map[string]monitorHostIdentity{request.Host: identity}
	if viewer == "" {
		result.Incomplete = true
		return result, nil
	}
	if len(identity.Owners) > 0 && viewer != monitorOwnerViewer(identity.Owners) {
		result.HostIdentities[request.Host] = monitorHostIdentity{Viewer: viewer}
		return result, errors.New("active account changed after repository scope discovery")
	}
	return result, nil
}

func setMonitorDiscoveredIdentity(result *monitorFetchResult, host string, owners []string) {
	identity := result.HostIdentities[host]
	if identity.Viewer != "" {
		return
	}
	if result.HostIdentities == nil {
		result.HostIdentities = make(map[string]monitorHostIdentity)
	}
	result.HostIdentities[host] = monitorHostIdentity{Viewer: strings.ToLower(monitorOwnerViewer(owners)), Owners: normalizedMonitorOwners(owners)}
}

func mergeMonitorIdentities(result, scope *monitorFetchResult) {
	if scope == nil {
		return
	}
	if result.HostIdentities == nil {
		result.HostIdentities = make(map[string]monitorHostIdentity)
	}
	for host, identity := range scope.HostIdentities {
		previous := result.HostIdentities[host]
		if identity.Viewer == "" && previous.Viewer != "" {
			identity = previous
		}
		if previous.Viewer != "" && identity.Viewer != "" && previous.Viewer != identity.Viewer {
			result.Incomplete = true
			identity.Conflicted = true
		}
		identity.Conflicted = identity.Conflicted || previous.Conflicted
		result.HostIdentities[host] = identity
	}
}

func monitorIdentityChanged(previous, current monitorHostIdentity, owners bool) bool {
	if current.Conflicted || previous.Conflicted {
		return true
	}
	if current.Viewer == "" {
		return false
	}
	if previous.Viewer != current.Viewer {
		return true
	}
	return owners && len(current.Owners) > 0 && !slices.Equal(previous.Owners, current.Owners)
}

func monitorIdentitiesChanged(previous, current *monitorFetchResult, owners bool) bool {
	for host, identity := range current.HostIdentities {
		if slices.Contains(previous.HostScope, host) && monitorIdentityChanged(previous.HostIdentities[host], identity, owners) {
			return true
		}
	}
	return false
}

func monitorIdentitiesEqual(previous, current *monitorFetchResult, owners bool) bool {
	if len(current.HostIdentities) == 0 || len(previous.HostIdentities) != len(current.HostIdentities) {
		return false
	}
	for host, identity := range current.HostIdentities {
		if !monitorHostIdentitiesEqual(previous.HostIdentities[host], identity, owners) {
			return false
		}
	}
	return true
}

// Invalidate affected caches even when all searches fail after discovery.
// Unknown identities preserve cached data; they never qualify a fresh diff.
func (m *monitorModel) invalidateMonitorOwnerSnapshot(current *monitorFetchResult) {
	if m.data == nil || current == nil {
		return
	}
	pins := m.data.Pinned
	for repo, pin := range pins {
		latest := monitorPinnedScope(current.Pinned, repo)
		latest = monitorPinRefreshIdentity(latest, current)
		if monitorIdentitiesChanged(pin, latest, false) {
			delete(pins, repo)
		}
	}
	if monitorIdentitiesChanged(m.data, current, true) {
		m.data = unavailableMonitorScope(m.cfg, errors.New("account or organization scope changed; refresh pending"))
		m.clearMonitorSnapshotChanges()
	}
	m.data.Pinned = pins
}

func monitorPinRefreshIdentity(pin, global *monitorFetchResult) *monitorFetchResult {
	if pin == nil {
		return global
	}
	if pin.Error == "" {
		return pin
	}
	identity := &monitorFetchResult{HostIdentities: make(map[string]monitorHostIdentity)}
	mergeMonitorIdentities(identity, global)
	for host, actor := range pin.HostIdentities {
		if actor.Viewer != "" {
			identity.HostIdentities[host] = actor
		}
	}
	return identity
}

func monitorHostIdentitiesEqual(old, current monitorHostIdentity, owners bool) bool {
	if current.Conflicted || old.Conflicted || current.Viewer == "" || old.Viewer != current.Viewer {
		return false
	}
	return !owners || (len(current.Owners) > 0 && slices.Equal(old.Owners, current.Owners))
}
