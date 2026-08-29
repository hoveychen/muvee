package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoveychen/muvee/internal/store"
)

// settingEnabledProjectTypes is the system_settings key holding the
// platform-wide whitelist of project types users may create. Value is a
// comma-separated list of store.ProjectType values.
//
// An unset / empty value means "no restriction — every known type is
// allowed", which is what every pre-existing install has. Operators running
// on a small box typically narrow it to the types that never invoke the
// builder (e.g. "compose,image,domain_only"), since `deployment` and `build`
// both run `docker build` and are the memory-hungry ones.
const settingEnabledProjectTypes = "enabled_project_types"

// normaliseEnabledProjectTypes validates and canonicalises a comma-separated
// project-type whitelist. Empty input returns "" — the "no restriction"
// sentinel. Non-empty input is split, trimmed, lower-cased, deduplicated and
// ordered by store.AllProjectTypes; every token must be a known project type.
//
// Input that is non-empty but contains no actual type (e.g. ",, ") is an
// error rather than a silent fallback to "": collapsing it to "" would turn an
// admin's "allow nothing" into "allow everything", the exact inverse of what
// they asked for.
func normaliseEnabledProjectTypes(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	seen := make(map[store.ProjectType]bool)
	for _, tok := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(tok))
		if name == "" {
			continue
		}
		t := store.ProjectType(name)
		if !store.IsKnownProjectType(t) {
			return "", fmt.Errorf("%s contains unknown project type %q (known: %s)",
				settingEnabledProjectTypes, name, strings.Join(projectTypeNames(store.AllProjectTypes), ", "))
		}
		seen[t] = true
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("%s must name at least one project type (leave it empty to allow all)",
			settingEnabledProjectTypes)
	}
	var out []string
	for _, t := range store.AllProjectTypes {
		if seen[t] {
			out = append(out, string(t))
		}
	}
	return strings.Join(out, ","), nil
}

// parseEnabledProjectTypes turns a stored whitelist into the concrete set of
// allowed types. Empty / unparseable input yields every known type, so a
// corrupt settings row degrades to the permissive pre-feature behaviour
// instead of locking the platform out of project creation entirely.
func parseEnabledProjectTypes(raw string) []store.ProjectType {
	seen := make(map[store.ProjectType]bool)
	for _, tok := range strings.Split(raw, ",") {
		t := store.ProjectType(strings.ToLower(strings.TrimSpace(tok)))
		if store.IsKnownProjectType(t) {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return append([]store.ProjectType(nil), store.AllProjectTypes...)
	}
	var out []store.ProjectType
	for _, t := range store.AllProjectTypes {
		if seen[t] {
			out = append(out, t)
		}
	}
	return out
}

// projectTypeNames renders types as plain strings, preserving the caller's
// order, for error messages and the JSON surface on /api/runtime/config.
func projectTypeNames(types []store.ProjectType) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, string(t))
	}
	return out
}

// enabledProjectTypes reads the platform whitelist from system_settings. A
// read error is treated as "unset" (allow everything) for the same reason
// parseEnabledProjectTypes is permissive: a transient DB hiccup must not stop
// admins from creating projects.
func (s *Server) enabledProjectTypes(ctx context.Context) []store.ProjectType {
	v, err := s.store.GetSetting(ctx, settingEnabledProjectTypes)
	if err != nil {
		return append([]store.ProjectType(nil), store.AllProjectTypes...)
	}
	return parseEnabledProjectTypes(v)
}

// checkProjectTypeEnabled returns a user-facing error when t is not on the
// platform whitelist. Applies to every caller including admins: narrowing the
// list is itself an admin action, so an admin who needs a disabled type
// re-enables it in /admin/settings rather than getting an invisible bypass.
func (s *Server) checkProjectTypeEnabled(ctx context.Context, t store.ProjectType) error {
	allowed := s.enabledProjectTypes(ctx)
	for _, a := range allowed {
		if a == t {
			return nil
		}
	}
	return fmt.Errorf("project type %q is disabled on this platform (allowed: %s)",
		t, strings.Join(projectTypeNames(allowed), ", "))
}
