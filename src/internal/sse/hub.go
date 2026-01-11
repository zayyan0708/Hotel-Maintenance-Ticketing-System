package sse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type Hub struct {
	logger *log.Logger

	register   chan chan []byte
	unregister chan chan []byte
	broadcast  chan []byte

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewHub(logger *log.Logger) *Hub {
	return &Hub{
		logger:     logger,
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		broadcast:  make(chan []byte, 100),
		clients:    make(map[chan []byte]struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case ch := <-h.register:
			h.mu.Lock()
			h.clients[ch] = struct{}{}
			h.mu.Unlock()
		case ch := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[ch]; ok {
				delete(h.clients, ch)
				close(ch)
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.Lock()
			for ch := range h.clients {
				select {
				case ch <- msg:
				default:
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) Broadcast(b []byte) {
	if !json.Valid(b) {
		b, _ = json.Marshal(map[string]any{
			"event":   "raw",
			"payload": string(b),
		})
	}
	h.broadcast <- append([]byte(nil), b...)
}

func (h *Hub) SSEHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		client := make(chan []byte, 25)
		h.register <- client
		defer func() { h.unregister <- client }()

		writeSSE(w, []byte(`{"event":"connected"}`))
		flusher.Flush()

		keepAlive := time.NewTicker(15 * time.Second)
		defer keepAlive.Stop()

		notify := r.Context().Done()
		bw := bufio.NewWriter(w)

		for {
			select {
			case <-notify:
				return
			case <-keepAlive.C:
				_, _ = bw.WriteString(": keep-alive\n\n")
				_ = bw.Flush()
				flusher.Flush()
			case msg, ok := <-client:
				if !ok {
					return
				}
				writeSSEBuffered(bw, msg)
				_ = bw.Flush()
				flusher.Flush()
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, data []byte) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", bytes.ReplaceAll(data, []byte("\n"), []byte("")))
}

func writeSSEBuffered(w *bufio.Writer, data []byte) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", bytes.ReplaceAll(data, []byte("\n"), []byte("")))
}
