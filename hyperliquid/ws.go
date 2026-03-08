package hyperliquid

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingInterval        = 50 * time.Second
	aliveTimeout        = 70 * time.Second // must receive something within this window (ping response, data, etc.)
	reconnectBackoff    = 1 * time.Second
	maxReconnectBackoff = 30 * time.Second
)

// newWsConn creates a websocket connection and starts background goroutines
// (readLoop, pingLoop) using the client's background context.
// The caller's ctx is ignored for the WS lifecycle; use Close() to shut down.
func (c *Client) newWsConn(ctx context.Context, subType string, params map[string]string) (*wsConn, error) {
	wc := &wsConn{
		wsUrl:     c.wsUrl,
		subType:   subType,
		subParams: params,
		msgCh:     make(chan []byte, 64),
	}
	if err := wc.dial(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.conns = append(c.conns, wc)
	c.mu.Unlock()

	go wc.readLoop(c.bgCtx)
	go wc.pingLoop(c.bgCtx)

	return wc, nil
}

// Ping sends a ping on all active websocket connections.
func (c *Client) Ping(ctx context.Context) error {
	c.mu.Lock()
	conns := make([]*wsConn, len(c.conns))
	copy(conns, c.conns)
	c.mu.Unlock()

	for _, wc := range conns {
		if err := wc.ping(); err != nil {
			return err
		}
	}
	return nil
}

// Reconnect closes and re-establishes all active websocket connections.
func (c *Client) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	conns := make([]*wsConn, len(c.conns))
	copy(conns, c.conns)
	c.mu.Unlock()

	for _, wc := range conns {
		if err := wc.reconnect(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (wc *wsConn) dial() error {
	conn, _, err := websocket.DefaultDialer.Dial(wc.wsUrl, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	// Only subscribe if subType is set (skip for order posting connections).
	if wc.subType != "" {
		subMap := map[string]string{"type": wc.subType}
		for k, v := range wc.subParams {
			subMap[k] = v
		}
		sub := wsSubscription{
			Method:       "subscribe",
			Subscription: subMap,
		}
		if err := conn.WriteJSON(sub); err != nil {
			conn.Close()
			return fmt.Errorf("websocket subscribe: %w", err)
		}
	}

	wc.mu.Lock()
	wc.conn = conn
	wc.mu.Unlock()
	wc.resetReadDeadline()
	return nil
}

// getOrderConn lazily creates a websocket connection for posting orders.
// Uses the client's background context so goroutines outlive any individual order.
func (c *Client) getOrderConn(ctx context.Context) (*wsConn, error) {
	c.orderConnOnce.Do(func() {
		wc, err := c.newWsConn(ctx, "", nil)
		if err != nil {
			c.orderConnErr = err
			return
		}
		c.orderConn = wc
		go c.orderResponseLoop(c.bgCtx)
	})
	return c.orderConn, c.orderConnErr
}

// WarmOrderConn eagerly establishes the order WebSocket connection and
// pre-loads the asset index map so the first order doesn't pay for
// dial + TLS handshake + metadata HTTP fetches.
func (c *Client) WarmOrderConn() error {
	if _, err := c.getOrderConn(c.bgCtx); err != nil {
		return err
	}
	return c.loadAssetMap(c.bgCtx)
}

// resetReadDeadline extends the read deadline so stale connections are detected.
func (wc *wsConn) resetReadDeadline() {
	wc.mu.Lock()
	if wc.conn != nil {
		wc.conn.SetReadDeadline(time.Now().Add(aliveTimeout))
	}
	wc.mu.Unlock()
}

func (wc *wsConn) ping() error {
	wc.mu.Lock()
	conn := wc.conn
	wc.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("hyperliquid: ping %s: no active connection", wc.subType)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"method":"ping"}`)); err != nil {
		return fmt.Errorf("hyperliquid: ping %s: %w", wc.subType, err)
	}
	return nil
}

func (wc *wsConn) reconnect(ctx context.Context) error {
	wc.mu.Lock()
	old := wc.conn
	if old != nil {
		old.Close()
	}
	wc.conn = nil
	wc.mu.Unlock()

	backoff := reconnectBackoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Another goroutine may have already reconnected.
		wc.mu.Lock()
		if wc.conn != nil {
			wc.mu.Unlock()
			return nil
		}
		wc.mu.Unlock()

		if err := wc.dial(); err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxReconnectBackoff)
			continue
		}
		return nil
	}
}

func (wc *wsConn) readLoop(ctx context.Context) {
	defer close(wc.msgCh)

	for {
		select {
		case <-ctx.Done():
			wc.mu.Lock()
			if wc.conn != nil {
				wc.conn.Close()
			}
			wc.mu.Unlock()
			return
		default:
		}

		wc.mu.Lock()
		conn := wc.conn
		wc.mu.Unlock()

		if conn == nil {
			if err := wc.reconnect(ctx); err != nil {
				return
			}
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Only nil/close the conn if it's still the one we were reading from.
			// If Reconnect() already replaced it, just loop back to read from the new one.
			wc.mu.Lock()
			if wc.conn == conn {
				conn.Close()
				wc.conn = nil
			}
			wc.mu.Unlock()
			continue
		}

		wc.resetReadDeadline()

		select {
		case wc.msgCh <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func (wc *wsConn) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := wc.ping(); err != nil {
				// Close the connection to unblock readLoop, which will trigger reconnection.
				wc.mu.Lock()
				if wc.conn != nil {
					wc.conn.Close()
					wc.conn = nil
				}
				wc.mu.Unlock()
			}
		}
	}
}
