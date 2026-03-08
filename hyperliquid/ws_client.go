package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	exchangeclients "github.com/orayaprolu/exchange-clients"
)

// buildOrderAction converts a PlaceOrderRequest into a wsOrderAction.
func (c *Client) buildOrderAction(ctx context.Context, req exchangeclients.PlaceOrderRequest) (wsOrderAction, int, error) {
	assetIdx, err := c.AssetIndex(ctx, c.coin(req.Coin))
	if err != nil {
		return wsOrderAction{}, 0, err
	}
	order, err := buildWsOrder(assetIdx, req.IsBuy, req.Price.String(), req.Size.String(), req.ReduceOnly, req.OrderType, req.TimeInForce, req.TriggerPx.String(), req.IsMarket, req.Cloid)
	if err != nil {
		return wsOrderAction{}, 0, err
	}
	return wsOrderAction{
		Type:     "order",
		Orders:   []wsOrder{order},
		Grouping: "na",
	}, assetIdx, nil
}

// buildWsOrder builds a wsOrder from the common order fields.
func buildWsOrder(assetIdx int, isBuy bool, price, size string, reduceOnly bool, orderType exchangeclients.OrderType, tif exchangeclients.TIF, triggerPx string, isMarket bool, cloid string) (wsOrder, error) {
	order := wsOrder{
		A: assetIdx,
		B: isBuy,
		P: price,
		S: size,
		R: reduceOnly,
		C: cloid,
	}

	switch orderType {
	case exchangeclients.OrderTypeLimit:
		t := string(tif)
		if t == "" {
			t = string(exchangeclients.TIFGtc)
		}
		order.T = wsOrderType{Limit: &wsLimitType{TIF: t}}
	case exchangeclients.OrderTypeTriggerStopLoss:
		order.T = wsOrderType{Trigger: &wsTriggerType{
			TriggerPx: triggerPx, IsMarket: isMarket, TPSL: "sl",
		}}
	case exchangeclients.OrderTypeTriggerTakeProfit:
		order.T = wsOrderType{Trigger: &wsTriggerType{
			TriggerPx: triggerPx, IsMarket: isMarket, TPSL: "tp",
		}}
	default:
		return wsOrder{}, fmt.Errorf("unsupported order type: %s", orderType)
	}
	return order, nil
}

// buildModifyAction converts a ModifyOrderRequest into a wsModifyAction.
func (c *Client) buildModifyAction(ctx context.Context, req exchangeclients.ModifyOrderRequest) (wsModifyAction, error) {
	assetIdx, err := c.AssetIndex(ctx, c.coin(req.Coin))
	if err != nil {
		return wsModifyAction{}, err
	}
	order, err := buildWsOrder(assetIdx, req.IsBuy, req.Price.String(), req.Size.String(), req.ReduceOnly, req.OrderType, req.TimeInForce, req.TriggerPx.String(), req.IsMarket, req.Cloid)
	if err != nil {
		return wsModifyAction{}, err
	}
	return wsModifyAction{
		Type:  "modify",
		OID:   uint64(req.OrderID),
		Order: order,
	}, nil
}

// buildCancelAction converts a CancelOrderRequest into a wsCancelAction.
func (c *Client) buildCancelAction(ctx context.Context, req exchangeclients.CancelOrderRequest) (wsCancelAction, error) {
	assetIdx, err := c.AssetIndex(ctx, c.coin(req.Coin))
	if err != nil {
		return wsCancelAction{}, err
	}
	return wsCancelAction{
		Type:    "cancel",
		Cancels: []wsCancel{{A: assetIdx, O: uint64(req.OrderID)}},
	}, nil
}

// sendWsAction signs and sends an action over the order websocket, returning the raw response.
func (c *Client) sendWsAction(ctx context.Context, action any) (wsPostResponse, error) {
	if c.privateKey == nil {
		return wsPostResponse{}, fmt.Errorf("hyperliquid: private key required")
	}

	oc, err := c.getOrderConn(ctx)
	if err != nil {
		return wsPostResponse{}, fmt.Errorf("hyperliquid: order conn: %w", err)
	}

	nonce := time.Now().UnixMilli()
	sig, err := signAction(c.privateKey, action, nonce, nil)
	if err != nil {
		return wsPostResponse{}, fmt.Errorf("hyperliquid: sign: %w", err)
	}

	id := c.nextID.Add(1)
	postReq := wsPostRequest{
		Method: "post",
		ID:     id,
		Request: wsActionPayload{
			Type: "action",
			Payload: wsSignedAction{
				Action:    action,
				Nonce:     nonce,
				Signature: sig,
			},
		},
	}

	respCh := make(chan wsPostResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeOrder(ctx, oc, postReq); err != nil {
		return wsPostResponse{}, err
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		return wsPostResponse{}, ctx.Err()
	}
}

// writeOrder writes a request to the order connection, reconnecting once on failure.
func (c *Client) writeOrder(ctx context.Context, oc *wsConn, req wsPostRequest) error {
	oc.mu.Lock()
	conn := oc.conn
	oc.mu.Unlock()

	if conn == nil {
		if err := oc.reconnect(ctx); err != nil {
			return fmt.Errorf("hyperliquid: order reconnect: %w", err)
		}
	} else if err := conn.WriteJSON(req); err == nil {
		return nil // write succeeded
	} else {
		log.Printf("hyperliquid: ws write failed, reconnecting: %v", err)
	}

	// Reconnect if we haven't already, then retry once.
	if err := oc.reconnect(ctx); err != nil {
		return fmt.Errorf("hyperliquid: order reconnect: %w", err)
	}
	oc.mu.Lock()
	conn = oc.conn
	oc.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("hyperliquid: no active order connection after reconnect")
	}
	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("hyperliquid: ws write after reconnect: %w", err)
	}
	return nil
}

// PlaceOrderWs places an order over the websocket connection.
func (c *Client) PlaceOrderWs(ctx context.Context, req exchangeclients.PlaceOrderRequest) (exchangeclients.PlaceOrderResponse, error) {
	action, _, err := c.buildOrderAction(ctx, req)
	if err != nil {
		return exchangeclients.PlaceOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.sendWsAction(ctx, action)
	if err != nil {
		return exchangeclients.PlaceOrderResponse{}, err
	}
	return parseOrderResponse(resp), nil
}

// CancelOrderWs cancels an order over the websocket connection.
func (c *Client) CancelOrderWs(ctx context.Context, req exchangeclients.CancelOrderRequest) (exchangeclients.CancelOrderResponse, error) {
	action, err := c.buildCancelAction(ctx, req)
	if err != nil {
		return exchangeclients.CancelOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.sendWsAction(ctx, action)
	if err != nil {
		return exchangeclients.CancelOrderResponse{}, err
	}
	return parseCancelResponse(resp), nil
}

// ModifyOrderWs modifies an order over the websocket connection.
func (c *Client) ModifyOrderWs(ctx context.Context, req exchangeclients.ModifyOrderRequest) (exchangeclients.ModifyOrderResponse, error) {
	action, err := c.buildModifyAction(ctx, req)
	if err != nil {
		return exchangeclients.ModifyOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.sendWsAction(ctx, action)
	if err != nil {
		return exchangeclients.ModifyOrderResponse{}, err
	}
	return parseModifyResponse(resp), nil
}

// orderResponseLoop reads from the order connection and dispatches responses.
func (c *Client) orderResponseLoop(ctx context.Context) {
	oc := c.orderConn
	if oc == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-oc.msgCh:
			if !ok {
				return
			}

			var resp wsPostResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				log.Printf("hyperliquid: order response unmarshal error: %v", err)
				continue
			}

			id := resp.Data.ID
			if id == 0 {
				continue
			}

			c.pendingMu.Lock()
			ch, ok := c.pending[id]
			c.pendingMu.Unlock()

			if ok {
				select {
				case ch <- resp:
				default:
				}
			}
		}
	}
}

// parseWsPayload extracts statuses from the ws payload response.
// The response field can be either a structured object or a plain string.
func parseWsPayload(resp wsPostResponse) (status string, statuses []json.RawMessage, rawResp string) {
	p := resp.Data.Response.Payload
	status = p.Status

	var pr wsPayloadResponse
	if err := json.Unmarshal(p.Response, &pr); err == nil {
		statuses = pr.Data.Statuses
		return
	}

	// Response is a plain string (e.g. cancel returns "Cancel successful")
	var s string
	if err := json.Unmarshal(p.Response, &s); err == nil {
		rawResp = s
		return
	}

	rawResp = string(p.Response)
	return
}

func parseOrderResponse(resp wsPostResponse) exchangeclients.PlaceOrderResponse {
	// Debug: log raw response
	rawRespDebug, _ := json.Marshal(resp)
	log.Printf("DEBUG: Raw order response: %s", string(rawRespDebug))

	status, statuses, rawResp := parseWsPayload(resp)

	if status != "ok" {
		return exchangeclients.PlaceOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("payload status=%q, response=%s", status, rawResp),
		}
	}

	if len(statuses) == 0 {
		return exchangeclients.PlaceOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("empty statuses, response=%s", rawResp),
		}
	}

	var st wsOrderStatus
	if err := json.Unmarshal(statuses[0], &st); err != nil {
		return exchangeclients.PlaceOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("unmarshal order status: %v (raw: %s)", err, statuses[0]),
		}
	}

	if st.Error != "" {
		return exchangeclients.PlaceOrderResponse{Status: "error", Error: st.Error}
	}
	if st.Resting != nil {
		return exchangeclients.PlaceOrderResponse{
			Status:  "resting",
			OrderID: strconv.FormatInt(st.Resting.OID, 10),
			Cloid:   st.Resting.Cloid,
		}
	}
	if st.Filled != nil {
		return exchangeclients.PlaceOrderResponse{
			Status:  "filled",
			OrderID: strconv.FormatInt(st.Filled.OID, 10),
			Cloid:   st.Filled.Cloid,
		}
	}

	return exchangeclients.PlaceOrderResponse{Status: "unknown"}
}

func parseCancelResponse(resp wsPostResponse) exchangeclients.CancelOrderResponse {
	status, statuses, rawResp := parseWsPayload(resp)

	if status != "ok" {
		return exchangeclients.CancelOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("payload status=%q, response=%s", status, rawResp),
		}
	}

	// Cancel can return a plain string response like "Cancel successful"
	if rawResp != "" {
		return exchangeclients.CancelOrderResponse{Status: "success"}
	}

	if len(statuses) == 0 {
		return exchangeclients.CancelOrderResponse{Status: "error", Error: "empty cancel response"}
	}

	raw := statuses[0]

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "success" {
			return exchangeclients.CancelOrderResponse{Status: "success"}
		}
		return exchangeclients.CancelOrderResponse{Status: "error", Error: s}
	}

	var st wsOrderStatus
	if err := json.Unmarshal(raw, &st); err == nil && st.Error != "" {
		return exchangeclients.CancelOrderResponse{Status: "error", Error: st.Error}
	}

	return exchangeclients.CancelOrderResponse{Status: "error", Error: fmt.Sprintf("unexpected: %s", raw)}
}

func parseModifyResponse(resp wsPostResponse) exchangeclients.ModifyOrderResponse {
	status, statuses, rawResp := parseWsPayload(resp)

	if status != "ok" {
		return exchangeclients.ModifyOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("payload status=%q, response=%s", status, rawResp),
		}
	}

	// Successful modify returns {"type":"default"} with no statuses.
	// The new OID is delivered via the order update stream.
	if len(statuses) == 0 {
		return exchangeclients.ModifyOrderResponse{Status: "success"}
	}

	var st wsOrderStatus
	if err := json.Unmarshal(statuses[0], &st); err != nil {
		return exchangeclients.ModifyOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("unmarshal modify status: %v (raw: %s)", err, statuses[0]),
		}
	}

	if st.Error != "" {
		return exchangeclients.ModifyOrderResponse{Status: "error", Error: st.Error}
	}
	if st.Resting != nil {
		return exchangeclients.ModifyOrderResponse{
			Status:  "resting",
			OrderID: strconv.FormatInt(st.Resting.OID, 10),
		}
	}

	return exchangeclients.ModifyOrderResponse{Status: "success"}
}
