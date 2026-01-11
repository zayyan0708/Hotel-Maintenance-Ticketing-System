package tickets

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"

	"src/internal/authclient"
	"src/internal/mq"
)

type API struct {
	logger *log.Logger
	repo   *Repository
	mqtt   mqtt.Client
}

func NewAPI(logger *log.Logger, repo *Repository, mqttClient mqtt.Client) *API {
	return &API{logger: logger, repo: repo, mqtt: mqttClient}
}

type CreateTicketReq struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	// Room is NOT allowed from guest; admin could use a separate endpoint if needed.
}

type UpdateStatusReq struct {
	Status string `json:"status"`
}

type AssignReq struct {
	StaffUserID int64 `json:"staff_user_id"`
}

type EventPayload struct {
	Event      string           `json:"event"`
	Ticket     Ticket           `json:"ticket"`
	AssignedTo *authclient.User `json:"assigned_to,omitempty"`
}

func (a *API) ListTicketsForUser(w http.ResponseWriter, r *http.Request, u authclient.User) {
	var items []Ticket
	var err error

	switch u.Role {
	case authclient.RoleAdmin:
		items, err = a.repo.ListAll(r.Context())
	case authclient.RoleGuest:
		items, err = a.repo.ListByRoom(r.Context(), u.Room)
	case authclient.RoleStaff:
		items, err = a.repo.ListAssignedTo(r.Context(), u.ID)
	default:
		writeErr(w, http.StatusForbidden, "unknown role")
		return
	}

	if err != nil {
		a.logger.Printf("list tickets: %v", err)
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) CreateTicketAsGuest(w http.ResponseWriter, r *http.Request, u authclient.User) {
	if u.Role != authclient.RoleGuest {
		writeErr(w, http.StatusForbidden, "only guests can create tickets here")
		return
	}
	if u.Room == "" {
		writeErr(w, http.StatusForbidden, "guest room not set")
		return
	}

	var req CreateTicketReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !IsValidType(req.Type) {
		writeErr(w, http.StatusBadRequest, "invalid type (plumbing/ac/noise/cleaning/wifi/other)")
		return
	}
	if req.Description == "" {
		writeErr(w, http.StatusBadRequest, "description is required")
		return
	}

	t, err := a.repo.Create(r.Context(), Ticket{
		Type:            req.Type,
		Room:            u.Room, // enforced from session
		Description:     req.Description,
		Status:          StatusOpen,
		CreatedByUserID: u.ID,
	})
	if err != nil {
		a.logger.Printf("create ticket: %v", err)
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}

	a.publish(mq.TopicTicketCreated, EventPayload{Event: "created", Ticket: t})
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) GetTicket(w http.ResponseWriter, r *http.Request, u authclient.User) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}

	t, err := a.repo.Get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.logger.Printf("get ticket: %v", err)
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}

	// access control
	if !canView(u, t) {
		writeErr(w, http.StatusForbidden, "not allowed")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) UpdateStatus(w http.ResponseWriter, r *http.Request, u authclient.User) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}

	var req UpdateStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !IsValidStatus(req.Status) {
		writeErr(w, http.StatusBadRequest, "invalid status (OPEN/IN_PROGRESS/RESOLVED)")
		return
	}

	current, err := a.repo.Get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}

	// Admin can update any; Staff only assigned; Guest cannot update
	if u.Role == authclient.RoleGuest {
		writeErr(w, http.StatusForbidden, "guests cannot update status")
		return
	}
	if u.Role == authclient.RoleStaff {
		if current.AssignedToUserID == nil || *current.AssignedToUserID != u.ID {
			writeErr(w, http.StatusForbidden, "staff can update only assigned tickets")
			return
		}
	}

	updated, err := a.repo.UpdateStatus(r.Context(), id, req.Status)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.logger.Printf("update status: %v", err)
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}

	a.publish(mq.TopicTicketStatusUpdated, EventPayload{Event: "status_updated", Ticket: updated})
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) Assign(w http.ResponseWriter, r *http.Request, u authclient.User, assignedTo authclient.User) {
	if u.Role != authclient.RoleAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}

	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}

	var req AssignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.StaffUserID <= 0 {
		writeErr(w, http.StatusBadRequest, "staff_user_id required")
		return
	}

	// You already fetched/validated assignedTo in gateway before calling
	t, err := a.repo.Assign(r.Context(), id, req.StaffUserID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		a.logger.Printf("assign: %v", err)
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}

	a.publish(mq.TopicTicketAssigned, EventPayload{
		Event:      "assigned",
		Ticket:     t,
		AssignedTo: &assignedTo,
	})
	writeJSON(w, http.StatusOK, t)
}

func canView(u authclient.User, t Ticket) bool {
	switch u.Role {
	case authclient.RoleAdmin:
		return true
	case authclient.RoleGuest:
		return u.Room != "" && t.Room == u.Room
	case authclient.RoleStaff:
		return t.AssignedToUserID != nil && *t.AssignedToUserID == u.ID
	default:
		return false
	}
}

func (a *API) publish(topic string, payload EventPayload) {
	if a.mqtt == nil || !a.mqtt.IsConnected() {
		a.logger.Printf("mqtt not connected; skipping publish topic=%s", topic)
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		a.logger.Printf("marshal event: %v", err)
		return
	}
	tok := a.mqtt.Publish(topic, 1, false, b)
	tok.WaitTimeout(3 * time.Second)
	if err := tok.Error(); err != nil {
		a.logger.Printf("publish error topic=%s: %v", topic, err)
	}
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
