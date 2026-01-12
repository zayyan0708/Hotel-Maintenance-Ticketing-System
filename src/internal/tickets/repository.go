package tickets

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// InitSchema performs a tiny migration that works even if you ran the old schema before.
func InitSchema(db *sql.DB) error {
	// base table
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  room TEXT NOT NULL,
  description TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  created_by_user_id INTEGER NOT NULL DEFAULT 0,
  assigned_to_user_id INTEGER NULL
);
CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets(created_at);
CREATE INDEX IF NOT EXISTS idx_tickets_room ON tickets(room);
CREATE INDEX IF NOT EXISTS idx_tickets_assigned ON tickets(assigned_to_user_id);
`)
	if err != nil {
		return err
	}

	// migrate older versions by adding columns if missing
	cols, err := tableColumns(db, "tickets")
	if err != nil {
		return err
	}

	if !cols["created_by_user_id"] {
		if _, err := db.Exec(`ALTER TABLE tickets ADD COLUMN created_by_user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if !cols["assigned_to_user_id"] {
		if _, err := db.Exec(`ALTER TABLE tickets ADD COLUMN assigned_to_user_id INTEGER NULL`); err != nil {
			return err
		}
	}

	// --------------------
	// Chat messages table
	// --------------------
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS chat_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL,
  from_user_id INTEGER NOT NULL,
  from_username TEXT NOT NULL,
  from_role TEXT NOT NULL,
  message TEXT NOT NULL,
  sent_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_ticket_id ON chat_messages(ticket_id);
CREATE INDEX IF NOT EXISTS idx_chat_sent_at ON chat_messages(sent_at);
`)
	if err != nil {
		return err
	}

	return nil
}

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (r *Repository) Create(ctx context.Context, in Ticket) (Ticket, error) {
	in.CreatedAt = time.Now().UTC()
	if in.Status == "" {
		in.Status = StatusOpen
	}

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO tickets(type, room, description, status, created_at, created_by_user_id, assigned_to_user_id)
		 VALUES(?,?,?,?,?,?,?)`,
		in.Type, in.Room, in.Description, in.Status, in.CreatedAt.Format(time.RFC3339Nano), in.CreatedByUserID, in.AssignedToUserID,
	)
	if err != nil {
		return Ticket{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Ticket{}, err
	}
	in.ID = id
	return in, nil
}

func (r *Repository) Get(ctx context.Context, id int64) (Ticket, error) {
	var t Ticket
	var created string
	var assigned sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT id, type, room, description, status, created_at, created_by_user_id, assigned_to_user_id
		 FROM tickets WHERE id=?`, id,
	).Scan(&t.ID, &t.Type, &t.Room, &t.Description, &t.Status, &created, &t.CreatedByUserID, &assigned)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, sql.ErrNoRows
	}
	if err != nil {
		return Ticket{}, err
	}
	t.CreatedAt = parseTime(created)
	if assigned.Valid {
		v := assigned.Int64
		t.AssignedToUserID = &v
	}
	return t, nil
}

func (r *Repository) ListAll(ctx context.Context) ([]Ticket, error) {
	return r.list(ctx, `SELECT id, type, room, description, status, created_at, created_by_user_id, assigned_to_user_id
		 FROM tickets
		 ORDER BY datetime(created_at) DESC, id DESC`)
}

func (r *Repository) ListByRoom(ctx context.Context, room string) ([]Ticket, error) {
	return r.list(ctx, `SELECT id, type, room, description, status, created_at, created_by_user_id, assigned_to_user_id
		 FROM tickets WHERE room=?
		 ORDER BY datetime(created_at) DESC, id DESC`, room)
}

func (r *Repository) ListAssignedTo(ctx context.Context, staffUserID int64) ([]Ticket, error) {
	return r.list(ctx, `SELECT id, type, room, description, status, created_at, created_by_user_id, assigned_to_user_id
		 FROM tickets WHERE assigned_to_user_id=?
		 ORDER BY datetime(created_at) DESC, id DESC`, staffUserID)
}

func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string) (Ticket, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE tickets SET status=? WHERE id=?`, status, id)
	if err != nil {
		return Ticket{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Ticket{}, err
	}
	if n == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return r.Get(ctx, id)
}

func (r *Repository) Assign(ctx context.Context, id int64, staffUserID int64) (Ticket, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE tickets SET assigned_to_user_id=? WHERE id=?`, staffUserID, id)
	if err != nil {
		return Ticket{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Ticket{}, err
	}
	if n == 0 {
		return Ticket{}, sql.ErrNoRows
	}
	return r.Get(ctx, id)
}

func (r *Repository) list(ctx context.Context, q string, args ...any) ([]Ticket, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Ticket
	for rows.Next() {
		var t Ticket
		var created string
		var assigned sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Type, &t.Room, &t.Description, &t.Status, &created, &t.CreatedByUserID, &assigned); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(created)
		if assigned.Valid {
			v := assigned.Int64
			t.AssignedToUserID = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --------------------
// Chat repo methods
// --------------------

func (r *Repository) InsertChatMessage(ctx context.Context, m ChatMessage) (ChatMessage, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO chat_messages(ticket_id, from_user_id, from_username, from_role, message, sent_at)
		VALUES(?,?,?,?,?,?)
	`, m.TicketID, m.FromUserID, m.FromUsername, m.FromRole, m.Message, m.SentAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ChatMessage{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ChatMessage{}, err
	}
	m.ID = id
	return m, nil
}

func (r *Repository) ListChatMessages(ctx context.Context, ticketID int64, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, ticket_id, from_user_id, from_username, from_role, message, sent_at
		FROM chat_messages
		WHERE ticket_id=?
		ORDER BY id ASC
		LIMIT ?
	`, ticketID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var sent string
		if err := rows.Scan(&m.ID, &m.TicketID, &m.FromUserID, &m.FromUsername, &m.FromRole, &m.Message, &sent); err != nil {
			return nil, err
		}
		m.SentAt = parseTime(sent)
		out = append(out, m)
	}
	return out, rows.Err()
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
