package tickets

import "time"

type Ticket struct {
	ID               int64     `json:"id"`
	Type             string    `json:"type"`
	Room             string    `json:"room"`
	Description      string    `json:"description"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	CreatedByUserID  int64     `json:"created_by_user_id"`
	AssignedToUserID *int64    `json:"assigned_to_user_id,omitempty"`
}

const (
	StatusOpen       = "OPEN"
	StatusInProgress = "IN_PROGRESS"
	StatusResolved   = "RESOLVED"
)

func IsValidStatus(s string) bool {
	return s == StatusOpen || s == StatusInProgress || s == StatusResolved
}

func IsValidType(t string) bool {
	switch t {
	case "plumbing", "ac", "noise", "cleaning", "wifi", "other":
		return true
	default:
		return false
	}
}

// --------------------
// Chat (Option A)
// --------------------

type ChatMessage struct {
	ID           int64     `json:"id"`
	TicketID     int64     `json:"ticket_id"`
	FromUserID   int64     `json:"from_user_id"`
	FromUsername string    `json:"from_username"`
	FromRole     string    `json:"from_role"`
	Message      string    `json:"message"`
	SentAt       time.Time `json:"sent_at"`
}

type ChatEventPayload struct {
	Event        string    `json:"event"` // "chat_message"
	TicketID     int64     `json:"ticket_id"`
	FromUserID   int64     `json:"from_user_id"`
	FromUsername string    `json:"from_username"`
	FromRole     string    `json:"from_role"`
	Message      string    `json:"message"`
	SentAt       time.Time `json:"sent_at"`
}
