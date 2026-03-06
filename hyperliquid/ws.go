package hyperliquid

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingInterval        = 50 * time.Second
	reconnectBackoff    = 1 * time.Second
	maxReconnectBackoff = 30 * time.Second
)

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

	go wc.readLoop(ctx)
	go wc.pingLoop(ctx)

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
	return nil
}

// getOrderConn lazily creates a websocket connection for posting orders.
func (c *Client) getOrderConn(ctx context.Context) (*wsConn, error) {
	c.orderConnOnce.Do(func() {
		wc, err := c.newWsConn(ctx, "", nil)
		if err != nil {
			c.orderConnErr = err
			return
		}
		c.orderConn = wc
		go c.orderResponseLoop(ctx)
	})
	return c.orderConn, c.orderConnErr
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

		log.Printf("hyperliquid: reconnecting %s (backoff %v)", wc.subType, backoff)
		if err := wc.dial(); err != nil {
			log.Printf("hyperliquid: reconnect failed %s: %v", wc.subType, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxReconnectBackoff)
			continue
		}
		log.Printf("hyperliquid: reconnected %s", wc.subType)
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
			log.Printf("hyperliquid: %s read error: %v", wc.subType, err)
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
				log.Printf("%v", err)
			}
		}
	}
}
