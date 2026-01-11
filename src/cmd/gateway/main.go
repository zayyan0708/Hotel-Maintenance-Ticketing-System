package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "modernc.org/sqlite"

	"src/internal/authclient"
	"src/internal/config"
	"src/internal/mq"
	"src/internal/session"
	"src/internal/sse"
	"src/internal/tickets"
)

const sessionCookieName = "smarthotel_session"

func main() {
	cfg := config.LoadGateway()
	logger := log.New(os.Stdout, "[gateway] ", log.LstdFlags|log.Lmicroseconds)

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		logger.Fatalf("mkdir data dir: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		logger.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := tickets.InitSchema(db); err != nil {
		logger.Fatalf("init schema: %v", err)
	}

	repo := tickets.NewRepository(db)

	// SSE hub
	hub := sse.NewHub(logger)
	go hub.Run()

	// MQTT client (publish + subscribe)
	mqttClient, err := mq.Connect(mq.Config{
		BrokerURL: cfg.MQTTBroker,
		ClientID:  cfg.MQTTClientID,
		Logger:    logger,
	})
	if err != nil {
		logger.Fatalf("mqtt connect: %v", err)
	}
	defer mqttClient.Disconnect(250)

	// Subscribe to topics and broadcast to SSE clients
	subscribeAndBridge(logger, mqttClient, hub)

	// Auth client + session store
	authC := authclient.New(cfg.AuthServiceURL, cfg.AuthInternalKey)
	sessions := session.NewStore(12 * time.Hour)

	// Templates
	tmpl, err := template.ParseFiles(
		"web/templates/layout.html",
		"web/templates/login.html",
		"web/templates/guest.html",
		"web/templates/admin.html",
		"web/templates/staff.html",
	)
	if err != nil {
		logger.Fatalf("parse templates: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(20 * time.Second))
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: logger, NoColor: true}))

	// Static
	fs := http.FileServer(http.Dir("web/static"))
	r.Handle("/static/*", http.StripPrefix("/static/", fs))

	// Health
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"gateway"}`))
	})

	// Public page
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		_ = tmpl.ExecuteTemplate(w, "layout.html", map[string]any{
			"Title":   "SmartHotel — Login",
			"Content": "login.html",
		})
	})

	// Auth API
	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req authclient.LoginRequest
		if err := jsonDecode(r, &req); err != nil {
			writeErr(w, 400, "invalid json")
			return
		}
		u, err := authC.Login(req)
		if err != nil {
			writeErr(w, 401, "invalid credentials")
			return
		}

		ss, err := sessions.Create(u)
		if err != nil {
			writeErr(w, 500, "session error")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    ss.ID,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			// Secure: true (enable if https)
		})

		writeJSON(w, 200, map[string]any{"user": u})
	})

	r.Post("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			sessions.Delete(c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	r.Get("/api/me", func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r, sessions)
		if !ok {
			writeErr(w, 401, "not logged in")
			return
		}
		writeJSON(w, 200, u)
	})

	// SSE stream (admin + staff can open if logged in)
	r.Get("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		_, ok := currentUser(r, sessions)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		hub.SSEHandler()(w, r)
	})

	// Pages (protected)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r, sessions)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if u.Role != authclient.RoleGuest {
			// redirect to correct portal
			if u.Role == authclient.RoleAdmin {
				http.Redirect(w, r, "/admin", http.StatusFound)
				return
			}
			if u.Role == authclient.RoleStaff {
				http.Redirect(w, r, "/staff", http.StatusFound)
				return
			}
		}

		_ = tmpl.ExecuteTemplate(w, "layout.html", map[string]any{
			"Title":   "SmartHotel — Guest",
			"Content": "guest.html",
		})
	})

	r.Get("/admin", func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r, sessions)
		if !ok || u.Role != authclient.RoleAdmin {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		_ = tmpl.ExecuteTemplate(w, "layout.html", map[string]any{
			"Title":   "SmartHotel — Admin",
			"Content": "admin.html",
		})
	})

	r.Get("/staff", func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r, sessions)
		if !ok || u.Role != authclient.RoleStaff {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		_ = tmpl.ExecuteTemplate(w, "layout.html", map[string]any{
			"Title":   "SmartHotel — Staff",
			"Content": "staff.html",
		})
	})

	// Ticket API (protected)
	ticketAPI := tickets.NewAPI(logger, repo, mqttClient)

	r.Route("/api", func(r chi.Router) {
		r.Get("/tickets", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok {
				writeErr(w, 401, "unauthorized")
				return
			}
			ticketAPI.ListTicketsForUser(w, r, u)
		})

		r.Post("/tickets", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok {
				writeErr(w, 401, "unauthorized")
				return
			}
			ticketAPI.CreateTicketAsGuest(w, r, u)
		})

		r.Get("/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok {
				writeErr(w, 401, "unauthorized")
				return
			}
			ticketAPI.GetTicket(w, r, u)
		})

		r.Patch("/tickets/{id}/status", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok {
				writeErr(w, 401, "unauthorized")
				return
			}
			ticketAPI.UpdateStatus(w, r, u)
		})

		// Admin-only assign
		r.Patch("/tickets/{id}/assign", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok {
				writeErr(w, 401, "unauthorized")
				return
			}
			if u.Role != authclient.RoleAdmin {
				writeErr(w, 403, "admin only")
				return
			}

			// Read staff_user_id first, then fetch staff list (simpler: validate using auth service list)
			var req struct {
				StaffUserID int64 `json:"staff_user_id"`
			}
			if err := jsonDecode(r, &req); err != nil || req.StaffUserID <= 0 {
				writeErr(w, 400, "invalid json/staff_user_id")
				return
			}

			// Validate staff exists by listing staff and matching ID (small N, acceptable)
			staff, err := authC.ListUsersByRole(authclient.RoleStaff)
			if err != nil {
				writeErr(w, 502, "auth service unavailable")
				return
			}
			var assignedTo *authclient.User
			for _, s := range staff {
				if s.ID == req.StaffUserID {
					tmp := s
					assignedTo = &tmp
					break
				}
			}
			if assignedTo == nil {
				writeErr(w, 400, "staff user not found")
				return
			}

			// Rewind body approach: easiest is to call a helper that assigns directly:
			// We'll call repo.Assign here and publish event for correctness.
			// But we already wrote tickets.API.Assign expecting assignedTo user:
			// We'll simulate by reconstructing request for tickets API:
			r.Body.Close()
			// Create a new request body not needed, call API method with current assignedTo:
			// For simplicity, call internal assign function:
			assignedTicket, err := repo.Assign(r.Context(), mustParseID(chi.URLParam(r, "id")), req.StaffUserID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeErr(w, 404, "not found")
					return
				}
				writeErr(w, 500, "db error")
				return
			}
			// publish mqtt
			// reuse tickets API publish logic by direct publish here:
			payload := tickets.EventPayload{
				Event:      "assigned",
				Ticket:     assignedTicket,
				AssignedTo: assignedTo,
			}
			publishMQTT(logger, mqttClient, mq.TopicTicketAssigned, payload)
			writeJSON(w, 200, assignedTicket)
		})

		// Admin-only user management (creates guest/staff)
		r.Post("/admin/users", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok || u.Role != authclient.RoleAdmin {
				writeErr(w, 401, "unauthorized")
				return
			}
			var req authclient.CreateUserRequest
			if err := jsonDecode(r, &req); err != nil {
				writeErr(w, 400, "invalid json")
				return
			}
			// basic validation
			if req.Username == "" || req.Password == "" {
				writeErr(w, 400, "username and password required")
				return
			}
			if req.Role != authclient.RoleGuest && req.Role != authclient.RoleStaff && req.Role != authclient.RoleAdmin {
				writeErr(w, 400, "invalid role")
				return
			}
			if req.Role == authclient.RoleGuest && req.Room == "" {
				writeErr(w, 400, "room required for GUEST")
				return
			}

			created, err := authC.CreateUser(req)
			if err != nil {
				writeErr(w, 400, "could not create user (maybe username exists)")
				return
			}
			writeJSON(w, 201, map[string]any{"user": created})
		})

		r.Get("/admin/staff", func(w http.ResponseWriter, r *http.Request) {
			u, ok := currentUser(r, sessions)
			if !ok || u.Role != authclient.RoleAdmin {
				writeErr(w, 401, "unauthorized")
				return
			}
			staff, err := authC.ListUsersByRole(authclient.RoleStaff)
			if err != nil {
				writeErr(w, 502, "auth service unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"users": staff})
		})
	})

	srv := &http.Server{Addr: cfg.Addr, Handler: r}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		logger.Printf("listening on %s (db=%s, mqtt=%s, auth=%s)", cfg.Addr, cfg.DBPath, cfg.MQTTBroker, cfg.AuthServiceURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func subscribeAndBridge(logger *log.Logger, c mqtt.Client, hub *sse.Hub) {
	topics := []string{mq.TopicTicketCreated, mq.TopicTicketStatusUpdated, mq.TopicTicketAssigned}
	for _, topic := range topics {
		topic := topic
		token := c.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
			hub.Broadcast(msg.Payload())
		})
		token.Wait()
		if err := token.Error(); err != nil {
			logger.Printf("mqtt subscribe error topic=%s: %v", topic, err)
		} else {
			logger.Printf("mqtt subscribed topic=%s", topic)
		}
	}
}

func currentUser(r *http.Request, store *session.Store) (authclient.User, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return authclient.User{}, false
	}
	ss, ok := store.Get(c.Value)
	if !ok {
		return authclient.User{}, false
	}
	return ss.User, true
}

// helpers
func jsonDecode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func mustParseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

func publishMQTT(logger *log.Logger, c mqtt.Client, topic string, payload any) {
	if c == nil || !c.IsConnected() {
		logger.Printf("mqtt not connected; skipping publish topic=%s", topic)
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		logger.Printf("marshal event: %v", err)
		return
	}
	tok := c.Publish(topic, 1, false, b)
	tok.WaitTimeout(3 * time.Second)
	if err := tok.Error(); err != nil {
		logger.Printf("publish error topic=%s: %v", topic, err)
	}
}
