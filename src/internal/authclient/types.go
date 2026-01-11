package authclient

import "time"

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"` // GUEST, STAFF, ADMIN
	Room      string    `json:"room"` // only for GUEST
	CreatedAt time.Time `json:"created_at"`
}

const (
	RoleGuest = "GUEST"
	RoleStaff = "STAFF"
	RoleAdmin = "ADMIN"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	User User `json:"user"`
}

type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Room     string `json:"room,omitempty"`
}

type CreateUserResponse struct {
	User User `json:"user"`
}

type ListUsersResponse struct {
	Users []User `json:"users"`
}
