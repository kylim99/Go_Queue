package dashboard

import (
	"context"
	"log/slog"
)

// Hub는 WebSocket 클라이언트를 관리하고 메시지를 브로드캐스트하는 허브이다.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	logger     *slog.Logger
}

// NewHub는 새로운 Hub 인스턴스를 생성한다.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client, 256),
		unregister: make(chan *Client, 256),
		logger:     logger,
	}
}

// Run은 Hub의 메인 루프를 시작한다. 클라이언트 등록/해제 및 브로드캐스트를 처리한다.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.clients[client] = true
			h.logger.Debug("websocket client registered", "total", len(h.clients))
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				h.logger.Debug("websocket client unregistered", "total", len(h.clients))
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// Shutdown은 모든 WebSocket 클라이언트의 전송 채널을 닫아 연결을 종료한다.
func (h *Hub) Shutdown() {
	for client := range h.clients {
		close(client.send)
		delete(h.clients, client)
	}
	h.logger.Info("websocket hub shut down")
}
