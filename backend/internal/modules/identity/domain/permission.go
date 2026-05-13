package domain

import "github.com/google/uuid"

// Permission represents a controller-level access grant for a role.
// resource and action may be the wildcard "*" (manager role).
type Permission struct {
	RoleID   uuid.UUID
	Resource string
	Action   string
}

// FieldPolicy represents a visible field grant for a role on a resource.
// resource and field may be the wildcard "*" (manager role).
type FieldPolicy struct {
	RoleID   uuid.UUID
	Resource string
	Field    string
}

// PermSet is the resolved, union-ed set of permissions for one or more roles.
// It is computed at request time from role_permissions + role_field_policies
// (cached in Redis) and passed to the DTO projection layer.
//
// Default deny: Can and CanSeeField return false for any unregistered combination.
type PermSet struct {
	perms  map[string]map[string]bool // resource → action → true
	fields map[string]map[string]bool // resource → field → true
}

// NewPermSet constructs a PermSet from raw permission and field policy slices.
func NewPermSet(permissions []Permission, fieldPolicies []FieldPolicy) PermSet {
	ps := PermSet{
		perms:  make(map[string]map[string]bool),
		fields: make(map[string]map[string]bool),
	}
	for _, p := range permissions {
		if ps.perms[p.Resource] == nil {
			ps.perms[p.Resource] = make(map[string]bool)
		}
		ps.perms[p.Resource][p.Action] = true
	}
	for _, f := range fieldPolicies {
		if ps.fields[f.Resource] == nil {
			ps.fields[f.Resource] = make(map[string]bool)
		}
		ps.fields[f.Resource][f.Field] = true
	}
	return ps
}

// Can returns true when the role set allows the given resource+action combination.
func (ps PermSet) Can(resource, action string) bool {
	if ps.wildcardPerm() {
		return true
	}
	if actions, ok := ps.perms[resource]; ok {
		return actions[action] || actions["*"]
	}
	return false
}

// CanSeeField returns true when the role set grants visibility for the field.
func (ps PermSet) CanSeeField(resource, field string) bool {
	if ps.wildcardField() {
		return true
	}
	if fields, ok := ps.fields[resource]; ok {
		return fields[field] || fields["*"]
	}
	return false
}

// Merge returns a new PermSet that is the union of ps and other.
// Used to combine permissions across multiple active roles.
func (ps PermSet) Merge(other PermSet) PermSet {
	merged := PermSet{
		perms:  make(map[string]map[string]bool, len(ps.perms)+len(other.perms)),
		fields: make(map[string]map[string]bool, len(ps.fields)+len(other.fields)),
	}
	for _, src := range []map[string]map[string]bool{ps.perms, other.perms} {
		for res, actions := range src {
			if merged.perms[res] == nil {
				merged.perms[res] = make(map[string]bool)
			}
			for action := range actions {
				merged.perms[res][action] = true
			}
		}
	}
	for _, src := range []map[string]map[string]bool{ps.fields, other.fields} {
		for res, flds := range src {
			if merged.fields[res] == nil {
				merged.fields[res] = make(map[string]bool)
			}
			for field := range flds {
				merged.fields[res][field] = true
			}
		}
	}
	return merged
}

func (ps PermSet) wildcardPerm() bool {
	if star, ok := ps.perms["*"]; ok && star["*"] {
		return true
	}
	return false
}

func (ps PermSet) wildcardField() bool {
	if star, ok := ps.fields["*"]; ok && star["*"] {
		return true
	}
	return false
}
