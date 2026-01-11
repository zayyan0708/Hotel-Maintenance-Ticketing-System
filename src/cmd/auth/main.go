package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"src/internal/config"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	PassHash  string    `json:"-"`
	Role      string    `json:"role"`
	Room      string    `json:"room"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	RoleGuest = "GUEST"
	RoleStaff = "STAFF"
	RoleAdmin = "ADMIN"
)

type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type CreateUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Room     string `json:"room,omitempty"`
}

func main() {
	cfg := config.LoadAuth()
	logger := log.New(os.Stdout, "[auth] ", log.LstdFlags|log.Lmicroseconds)

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		logger.Fatalf("mkdir auth_data: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		logger.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := initSchema(db); err != nil {
		logger.Fatalf("init schema: %v", err)
	}

	// bootstrap admin
	if cfg.BootstrapAdmin {
		_ = ensureAdmin(db, cfg.BootstrapUser, cfg.BootstrapPass)
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: logger, NoColor: true}))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "service": "auth"})
	})

	// Public: login
	r.Post("/api/login", func(w http.ResponseWriter, r *http.Request) {
		var req LoginReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "invalid json")
			return
		}
		u, err := getByUsername(db, req.Username)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, 401, "invalid credentials")
			return
		}
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(req.Password)) != nil {
			writeErr(w, 401, "invalid credentials")
			return
		}
		writeJSON(w, 200, map[string]any{
			"user": map[string]any{
				"id":         u.ID,
				"username":   u.Username,
				"role":       u.Role,
				"room":       u.Room,
				"created_at": u.CreatedAt,
			},
		})
	})

	// Internal: create user, list users (protected by internal key)
	r.Post("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if !internalOK(r, cfg.InternalKey) {
			writeErr(w, 403, "forbidden")
			return
		}
		var req CreateUserReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "invalid json")
			return
		}
		if req.Username == "" || req.Password == "" {
			writeErr(w, 400, "username and password required")
			return
		}
		if req.Role != RoleGuest && req.Role != RoleStaff && req.Role != RoleAdmin {
			writeErr(w, 400, "invalid role")
			return
		}
		if req.Role == RoleGuest && req.Room == "" {
			writeErr(w, 400, "room required for guest")
			return
		}
		if req.Role != RoleGuest && req.Room != "" {
			req.Room = ""
		}

		ph, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		now := time.Now().UTC()

		res, err := db.Exec(`INSERT INTO users(username, password_hash, role, room, created_at) VALUES(?,?,?,?,?)`,
			req.Username, string(ph), req.Role, req.Room, now.Format(time.RFC3339Nano),
		)
		if err != nil {
			writeErr(w, 400, "could not create user (maybe username exists)")
			return
		}
		id, _ := res.LastInsertId()

		writeJSON(w, 201, map[string]any{
			"user": map[string]any{
				"id":         id,
				"username":   req.Username,
				"role":       req.Role,
				"room":       req.Room,
				"created_at": now,
			},
		})
	})

	r.Get("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if !internalOK(r, cfg.InternalKey) {
			writeErr(w, 403, "forbidden")
			return
		}
		role := r.URL.Query().Get("role")

		var rows *sql.Rows
		var err error
		if role != "" {
			rows, err = db.Query(`SELECT id, username, role, room, created_at FROM users WHERE role=? ORDER BY id ASC`, role)
		} else {
			rows, err = db.Query(`SELECT id, username, role, room, created_at FROM users ORDER BY id ASC`)
		}
		if err != nil {
			writeErr(w, 500, "db error")
			return
		}
		defer rows.Close()

		type outUser struct {
			ID        int64     `json:"id"`
			Username  string    `json:"username"`
			Role      string    `json:"role"`
			Room      string    `json:"room"`
			CreatedAt time.Time `json:"created_at"`
		}

		var out []outUser
		for rows.Next() {
			var u outUser
			var created string
			if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Room, &created); err != nil {
				writeErr(w, 500, "db error")
				return
			}
			u.CreatedAt = parseTime(created)
			out = append(out, u)
		}

		writeJSON(w, 200, map[string]any{"users": out})
	})

	srv := &http.Server{Addr: cfg.Addr, Handler: r}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		logger.Printf("listening on %s (db=%s)", cfg.Addr, cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func internalOK(r *http.Request, key string) bool {
	return key != "" && r.Header.Get("X-Internal-Key") == key
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  room TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
`)
	return err
}

func ensureAdmin(db *sql.DB, user, pass string) error {
	// create only if not exists
	var id int64
	err := db.QueryRow(`SELECT id FROM users WHERE username=?`, user).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	ph, _ := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	now := time.Now().UTC()
	_, err = db.Exec(`INSERT INTO users(username, password_hash, role, room, created_at) VALUES(?,?,?,?,?)`,
		user, string(ph), RoleAdmin, "", now.Format(time.RFC3339Nano),
	)
	return err
}

func getByUsername(db *sql.DB, username string) (User, error) {
	var u User
	var created string
	err := db.QueryRow(`SELECT id, username, password_hash, role, room, created_at FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PassHash, &u.Role, &u.Room, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = parseTime(created)
	return u, nil
}

func parseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Now().UTC()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
