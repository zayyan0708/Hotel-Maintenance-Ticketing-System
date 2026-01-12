package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"src/internal/config"
	"src/internal/mq"
)

type EventRecord struct {
	ReceivedAt time.Time       `json:"received_at"`
	Topic      string          `json:"topic"`
	Payload    json.RawMessage `json:"payload"`
}

type RingBuffer struct {
	max int
	arr []EventRecord
}

func NewRingBuffer(max int) *RingBuffer {
	if max <= 0 {
		max = 50
	}
	return &RingBuffer{max: max, arr: make([]EventRecord, 0, max)}
}

func (rb *RingBuffer) Add(e EventRecord) {
	if len(rb.arr) < rb.max {
		rb.arr = append(rb.arr, e)
		return
	}
	copy(rb.arr, rb.arr[1:])
	rb.arr[len(rb.arr)-1] = e
}

func (rb *RingBuffer) Snapshot() []EventRecord {
	out := make([]EventRecord, len(rb.arr))
	copy(out, rb.arr)
	return out
}

func main() {
	cfg := config.LoadNotifier()
	logger := log.New(os.Stdout, "[notifier] ", log.LstdFlags|log.Lmicroseconds)

	bufSize := 50
	if cfg.EventBufferSize != "" {
		if n, err := strconv.Atoi(cfg.EventBufferSize); err == nil && n > 0 {
			bufSize = n
		}
	}
	rb := NewRingBuffer(bufSize)

	client, err := mq.Connect(mq.Config{
		BrokerURL: cfg.MQTTBroker,
		ClientID:  cfg.MQTTClientID,
		Logger:    logger,
	})
	if err != nil {
		logger.Fatalf("mqtt connect: %v", err)
	}
	defer client.Disconnect(250)

	subscribe := func(topic string) {
		token := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
			rec := EventRecord{
				ReceivedAt: time.Now().UTC(),
				Topic:      msg.Topic(),
				Payload:    json.RawMessage(append([]byte(nil), msg.Payload()...)),
			}
			rb.Add(rec)
			logger.Printf("ALERT topic=%s payload=%s", msg.Topic(), string(msg.Payload()))
		})
		token.Wait()
		if err := token.Error(); err != nil {
			logger.Printf("subscribe error topic=%s: %v", topic, err)
		} else {
			logger.Printf("subscribed topic=%s", topic)
		}
	}

	subscribe(mq.TopicTicketCreated)
	subscribe(mq.TopicTicketStatusUpdated)
	subscribe(mq.TopicTicketAssigned)

	// ✅ Chat events
	subscribe(mq.TopicChatTicketWildcard)

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"notifier"}`))
	})

	r.Get("/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":  len(rb.arr),
			"events": rb.Snapshot(),
		})
	})

	srv := &http.Server{Addr: cfg.Addr, Handler: r}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Printf("listening on %s (mqtt=%s)", cfg.Addr, cfg.MQTTBroker)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	logger.Printf("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Printf("stopped")
}
