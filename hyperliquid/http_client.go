package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	exchangeclients "github.com/orayaprolu/exchange-clients"
)

// postExchange signs an action and POSTs it to the /exchange endpoint.
func (c *Client) postExchange(ctx context.Context, action any) (httpExchangeResponse, error) {
	if c.privateKey == nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: private key required")
	}

	nonce := time.Now().UnixMilli()
	sig, err := signAction(c.privateKey, action, nonce, nil)
	if err != nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: sign: %w", err)
	}

	body := httpExchangeRequest{
		Action:    action,
		Nonce:     nonce,
		Signature: sig,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.exchangeUrl, bytes.NewReader(jsonBody))
	if err != nil {
		return httpExchangeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: exchange post: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: read response: %w", err)
	}

	var result httpExchangeResponse
	if err := json.Unmarshal(b, &result); err != nil {
		return httpExchangeResponse{}, fmt.Errorf("hyperliquid: unmarshal response: %w (body: %s)", err, b)
	}

	return result, nil
}

// PlaceOrder places an order via the HTTP exchange endpoint.
func (c *Client) PlaceOrder(ctx context.Context, req exchangeclients.PlaceOrderRequest) (exchangeclients.PlaceOrderResponse, error) {
	action, _, err := c.buildOrderAction(ctx, req)
	if err != nil {
		return exchangeclients.PlaceOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.postExchange(ctx, action)
	if err != nil {
		return exchangeclients.PlaceOrderResponse{}, err
	}
	return parseHTTPOrderResponse(resp), nil
}

// CancelOrder cancels an order via the HTTP exchange endpoint.
func (c *Client) CancelOrder(ctx context.Context, req exchangeclients.CancelOrderRequest) (exchangeclients.CancelOrderResponse, error) {
	action, err := c.buildCancelAction(ctx, req)
	if err != nil {
		return exchangeclients.CancelOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.postExchange(ctx, action)
	if err != nil {
		return exchangeclients.CancelOrderResponse{}, err
	}
	return parseHTTPCancelResponse(resp), nil
}

// ModifyOrder modifies an order via the HTTP exchange endpoint.
func (c *Client) ModifyOrder(ctx context.Context, req exchangeclients.ModifyOrderRequest) (exchangeclients.ModifyOrderResponse, error) {
	action, err := c.buildModifyAction(ctx, req)
	if err != nil {
		return exchangeclients.ModifyOrderResponse{}, fmt.Errorf("hyperliquid: %w", err)
	}

	resp, err := c.postExchange(ctx, action)
	if err != nil {
		return exchangeclients.ModifyOrderResponse{}, err
	}
	return parseHTTPModifyResponse(resp), nil
}

// GetOrderbook fetches the L2 orderbook snapshot via the HTTP info endpoint.
func (c *Client) GetOrderbook(ctx context.Context, pair string) (exchangeclients.Orderbook, error) {
	coin := c.coin(pair)
	payload := fmt.Sprintf(`{"type":"l2Book","coin":"%s"}`, coin)

	req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(payload))
	if err != nil {
		return exchangeclients.Orderbook{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return exchangeclients.Orderbook{}, fmt.Errorf("hyperliquid: l2Book: %w", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchangeclients.Orderbook{}, fmt.Errorf("hyperliquid: read l2Book: %w", err)
	}

	var raw wsBookMessage
	if err := json.Unmarshal(b, &raw.Data); err != nil {
		return exchangeclients.Orderbook{}, fmt.Errorf("hyperliquid: unmarshal l2Book: %w", err)
	}

	bids, err := convertLevels(raw.Data.Levels[0])
	if err != nil {
		return exchangeclients.Orderbook{}, fmt.Errorf("hyperliquid: parse bids: %w", err)
	}
	asks, err := convertLevels(raw.Data.Levels[1])
	if err != nil {
		return exchangeclients.Orderbook{}, fmt.Errorf("hyperliquid: parse asks: %w", err)
	}

	return exchangeclients.Orderbook{
		Coin: coin,
		Bids: bids,
		Asks: asks,
		Time: raw.Data.Time,
	}, nil
}

// parseHTTPStatuses extracts statuses from the HTTP response.
// The response field can be a structured object or a plain string (on error).
func parseHTTPStatuses(resp httpExchangeResponse) (statuses []json.RawMessage, rawResp string) {
	// Try structured: {"type":"order","data":{"statuses":[...]}}
	var structured struct {
		Type string `json:"type"`
		Data struct {
			Statuses []json.RawMessage `json:"statuses"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Response, &structured); err == nil {
		return structured.Data.Statuses, ""
	}

	// Plain string (e.g. error message or "Cancel successful")
	var s string
	if err := json.Unmarshal(resp.Response, &s); err == nil {
		return nil, s
	}

	return nil, string(resp.Response)
}

func parseHTTPOrderResponse(resp httpExchangeResponse) exchangeclients.PlaceOrderResponse {
	if resp.Status != "ok" {
		_, rawResp := parseHTTPStatuses(resp)
		return exchangeclients.PlaceOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("status=%q, response=%s", resp.Status, rawResp),
		}
	}

	statuses, rawResp := parseHTTPStatuses(resp)
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
			Error:  fmt.Sprintf("parse order status: %v (raw: %s)", err, statuses[0]),
		}
	}

	if st.Error != "" {
		return exchangeclients.PlaceOrderResponse{Status: "error", Error: st.Error}
	}
	if st.Resting != nil {
		return exchangeclients.PlaceOrderResponse{
			Status:  "resting",
			OrderID: fmt.Sprintf("%d", st.Resting.OID),
			Cloid:   st.Resting.Cloid,
		}
	}
	if st.Filled != nil {
		return exchangeclients.PlaceOrderResponse{
			Status:  "filled",
			OrderID: fmt.Sprintf("%d", st.Filled.OID),
			Cloid:   st.Filled.Cloid,
		}
	}

	return exchangeclients.PlaceOrderResponse{Status: "unknown"}
}

func parseHTTPCancelResponse(resp httpExchangeResponse) exchangeclients.CancelOrderResponse {
	if resp.Status != "ok" {
		_, rawResp := parseHTTPStatuses(resp)
		return exchangeclients.CancelOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("status=%q, response=%s", resp.Status, rawResp),
		}
	}

	statuses, rawResp := parseHTTPStatuses(resp)

	// Plain string response like "Cancel successful"
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

func parseHTTPModifyResponse(resp httpExchangeResponse) exchangeclients.ModifyOrderResponse {
	if resp.Status != "ok" {
		_, rawResp := parseHTTPStatuses(resp)
		return exchangeclients.ModifyOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("status=%q, response=%s", resp.Status, rawResp),
		}
	}

	// Successful modify returns {"type":"default"} with no statuses.
	// The new OID is delivered via the order update stream.
	statuses, _ := parseHTTPStatuses(resp)
	if len(statuses) == 0 {
		return exchangeclients.ModifyOrderResponse{Status: "success"}
	}

	var st wsOrderStatus
	if err := json.Unmarshal(statuses[0], &st); err != nil {
		return exchangeclients.ModifyOrderResponse{
			Status: "error",
			Error:  fmt.Sprintf("parse modify status: %v (raw: %s)", err, statuses[0]),
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
