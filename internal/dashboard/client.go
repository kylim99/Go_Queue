package dashboard

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

// Client는 WebSocket 연결 하나를 나타내며, Hub와 연결된 읽기/쓰기 고루틴을 관리한다.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// writePump은 Hub에서 수신한 메시지를 WebSocket으로 전송하는 고루틴이다.
func (c *Client) writePump(ctx context.Context) {
	defer func() {
		c.conn.Close(websocket.StatusNormalClosure, "")
		c.hub.unregister <- c
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-c.send:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.conn.Write(writeCtx, websocket.MessageText, message)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// readPump은 WebSocket에서 메시지를 읽어 연결 해제를 감지하는 고루틴이다.
func (c *Client) readPump(ctx context.Context) {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		_, _, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
	}
}
