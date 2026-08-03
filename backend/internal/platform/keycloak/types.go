package keycloak

// User is the subset of a Keycloak user representation this package needs.
type User struct {
	ID       string
	Username string
	Email    string
	Enabled  bool
}

// CreateUserRequest is the input for Client.CreateUser.
type CreateUserRequest struct {
	Email    string
	FullName string
}

// keycloakUserRepresentation mirrors Keycloak's UserRepresentation JSON shape
// (only the fields this package reads or writes).
type keycloakUserRepresentation struct {
	ID            string `json:"id,omitempty"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	FirstName     string `json:"firstName,omitempty"`
	Enabled       bool   `json:"enabled"`
	EmailVerified bool   `json:"emailVerified"`
}

func toUser(r keycloakUserRepresentation) User {
	return User{ID: r.ID, Username: r.Username, Email: r.Email, Enabled: r.Enabled}
}
