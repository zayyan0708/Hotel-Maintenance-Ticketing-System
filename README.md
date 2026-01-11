# Hotel-Maintenance-Ticketing-System - Distributed Dorm Issue Reporting & Real-Time Alerts (Go + REST + MQTT)

A small, exam-ready distributed system in Go:

- Students submit dorm issues via a web UI.
- Issues are stored in SQLite.
- On create/status updates, the Gateway publishes MQTT events.
- The Admin dashboard updates in real-time via **SSE** (Gateway subscribes to MQTT and streams events to browsers).
- A separate Notifier service also subscribes to MQTT and exposes `/events` for debugging.

---

## Architecture (ASCII)

                   +-------------------+
Browser (Student)  |  Gateway Service  |   SQLite
  /                |  REST + Templates |<--------+
  POST/GET Issues  |  + MQTT Publisher |         |
                   |  + MQTT Subscriber|         |
Browser (Admin)    |  + SSE (/admin)   |         |
  /admin  <--------|  (MQTT -> SSE)    |         |
   live events     +---------+---------+         |
                             |                   |
                             | MQTT (QoS 1)      |
                             v                   |
                      +-------------+            |
                      | Mosquitto   |            |
                      | MQTT Broker |            |
                      +------+------+\-----------+
                             |
                             | MQTT Subscriptions
                             v
                    +-------------------+
                    | Notifier Service  |
                    | MQTT Subscriber   |
                    | + /events, /health|
                    +-------------------+

---

## Components

### 1) Gateway Service
- Serves:
  - `/` student page: create issue + list issues
  - `/admin` admin dashboard: list issues + update status + **live event stream**
- REST API:
  - `POST /api/issues`
  - `GET /api/issues`
  - `GET /api/issues/{id}`
  - `PATCH /api/issues/{id}`
  - `GET /health`
- DB: SQLite (pure Go driver `modernc.org/sqlite`)
- MQTT:
  - Publishes:
    - `src/issues/created`
    - `src/issues/status_updated`
  - Subscribes to both topics and forwards messages to admin browsers via SSE.

### 2) Notifier Service
- Subscribes to both MQTT topics
- Logs events and keeps last N events in memory
- REST:
  - `GET /events`
  - `GET /health`

### 3) MQTT Broker
- Mosquitto via Docker Compose
- Listener: 1883 (TCP), 9001 (WebSocket enabled for completeness)

---

## Configuration

Environment variables:

Gateway:
- `GATEWAY_ADDR` (default `:8080`)
- `DB_PATH` (default `./data/src.db`)
- `MQTT_BROKER` (default `tcp://localhost:1883`)
- `MQTT_CLIENT_ID` (default `src-gateway`)

Notifier:
- `NOTIFIER_ADDR` (default `:8081`)
- `MQTT_BROKER` (default `tcp://localhost:1883`)
- `MQTT_CLIENT_ID` (default `src-notifier`)
- `EVENT_BUFFER_SIZE` (default `50`)

---

## Run with Docker (recommended)

### 1) Start everything
```bash
docker compose -f deploy/docker-compose.yml up --build
