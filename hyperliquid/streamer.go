package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	exchangeclients "github.com/orayaprolu/exchange-clients"

	"github.com/shopspring/decimal"
)

func (c *Client) StreamOrderbook(ctx context.Context, pair string) (<-chan exchangeclients.Orderbook, error) {
	coin, err := c.streamCoin(ctx, pair)
	if err != nil {
		return nil, err
	}
	wc, err := c.newWsConn(ctx, "l2Book", map[string]string{"coin": coin})
	if err != nil {
		return nil, err
	}

	ch := make(chan exchangeclients.Orderbook, 64)
	go func() {
		defer close(ch)
		for msg := range wc.msgCh {
			var raw wsBookMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				log.Printf("hyperliquid: orderbook unmarshal error for %s: %v", pair, err)
				continue
			}
			if raw.Channel != "l2Book" {
				continue
			}

			bids, err := convertLevels(raw.Data.Levels[0])
			if err != nil {
				log.Printf("hyperliquid: orderbook bid parse error for %s: %v", pair, err)
				continue
			}
			asks, err := convertLevels(raw.Data.Levels[1])
			if err != nil {
				log.Printf("hyperliquid: orderbook ask parse error for %s: %v", pair, err)
				continue
			}

			select {
			case ch <- exchangeclients.Orderbook{Coin: raw.Data.Coin, Bids: bids, Asks: asks, Time: raw.Data.Time}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (c *Client) StreamBBO(ctx context.Context, pair string) (<-chan exchangeclients.BBO, error) {
	if strings.Contains(pair, "/") {
		return nil, fmt.Errorf("hyperliquid: bbo stream is not supported for spot pairs (use StreamOrderbook instead)")
	}
	coin, err := c.streamCoin(ctx, pair)
	if err != nil {
		return nil, err
	}
	wc, err := c.newWsConn(ctx, "bbo", map[string]string{"coin": coin})
	if err != nil {
		return nil, err
	}

	ch := make(chan exchangeclients.BBO, 64)
	go func() {
		defer close(ch)
		for msg := range wc.msgCh {
			var raw wsBboMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				log.Printf("hyperliquid: bbo unmarshal error for %s: %v", pair, err)
				continue
			}
			if raw.Channel != "bbo" {
				continue
			}

			bbo := exchangeclients.BBO{Coin: raw.Data.Coin, Time: raw.Data.Time}
			if bid := raw.Data.Bbo[0]; bid != nil {
				bbo.BidPrice, bbo.BidSize, err = parseLevel(bid)
				if err != nil {
					log.Printf("hyperliquid: bbo bid parse error for %s: %v", pair, err)
					continue
				}
			}
			if ask := raw.Data.Bbo[1]; ask != nil {
				bbo.AskPrice, bbo.AskSize, err = parseLevel(ask)
				if err != nil {
					log.Printf("hyperliquid: bbo ask parse error for %s: %v", pair, err)
					continue
				}
			}

			select {
			case ch <- bbo:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (c *Client) StreamOrderUpdates(ctx context.Context) (<-chan exchangeclients.OrderUpdate, error) {
	if c.privateKey == nil {
		return nil, fmt.Errorf("hyperliquid: private key required for order updates")
	}

	wc, err := c.newWsConn(ctx, "orderUpdates", map[string]string{"user": c.address.Hex()})
	if err != nil {
		return nil, err
	}

	ch := make(chan exchangeclients.OrderUpdate, 64)
	go func() {
		defer close(ch)
		for msg := range wc.msgCh {
			var raw wsOrderUpdateMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				continue // subscription ack or non-array message
			}
			if raw.Channel != "orderUpdates" {
				continue
			}

			for _, wu := range raw.Data {
				limitPx, err := decimal.NewFromString(wu.Order.LimitPx)
				if err != nil {
					log.Printf("hyperliquid: order update parse limitPx %q: %v", wu.Order.LimitPx, err)
					continue
				}
				sz, err := decimal.NewFromString(wu.Order.Sz)
				if err != nil {
					log.Printf("hyperliquid: order update parse sz %q: %v", wu.Order.Sz, err)
					continue
				}
				origSz, err := decimal.NewFromString(wu.Order.OrigSz)
				if err != nil {
					log.Printf("hyperliquid: order update parse origSz %q: %v", wu.Order.OrigSz, err)
					continue
				}

				update := exchangeclients.OrderUpdate{
					Coin:           wu.Order.Coin,
					Side:           wu.Order.Side,
					LimitPx:        limitPx,
					Size:           sz,
					OrigSize:       origSz,
					OrderID:        wu.Order.OID,
					Cloid:          wu.Order.Cloid,
					Timestamp:      wu.Order.Timestamp,
					Status:         wu.Status,
					StatusTimestamp: wu.StatusTimestamp,
				}

				select {
				case ch <- update:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

func (c *Client) StreamPositions(ctx context.Context) (<-chan exchangeclients.ClearinghouseState, error) {
	if c.privateKey == nil {
		return nil, fmt.Errorf("hyperliquid: private key required for position streaming")
	}

	params := map[string]string{"user": c.address.Hex()}
	if c.exchange != "" {
		params["dex"] = c.exchange
	}

	wc, err := c.newWsConn(ctx, "clearinghouseState", params)
	if err != nil {
		return nil, err
	}

	ch := make(chan exchangeclients.ClearinghouseState, 64)
	go func() {
		defer close(ch)
		for msg := range wc.msgCh {
			var raw wsClearinghouseMessage
			if err := json.Unmarshal(msg, &raw); err != nil {
				continue
			}
			if raw.Channel != "clearinghouseState" {
				continue
			}

			state := exchangeclients.ClearinghouseState{
				MarginSummary: exchangeclients.MarginSummary{
					AccountValue:    decOrZero(raw.Data.ClearinghouseState.MarginSummary.AccountValue),
					TotalNtlPos:     decOrZero(raw.Data.ClearinghouseState.MarginSummary.TotalNtlPos),
					TotalRawUsd:     decOrZero(raw.Data.ClearinghouseState.MarginSummary.TotalRawUsd),
					TotalMarginUsed: decOrZero(raw.Data.ClearinghouseState.MarginSummary.TotalMarginUsed),
				},
			}

			for _, ap := range raw.Data.ClearinghouseState.AssetPositions {
				p := ap.Position
				state.Positions = append(state.Positions, exchangeclients.Position{
					Coin:           p.Coin,
					Size:           decOrZero(p.Szi),
					EntryPx:        decOrZero(p.EntryPx),
					MarkPx:         decOrZero(p.MarkPx),
					LiquidationPx:  decOrZero(p.LiquidationPx),
					UnrealizedPnl:  decOrZero(p.UnrealizedPnl),
					ReturnOnEquity: decOrZero(p.ReturnOnEquity),
					MarginUsed:     decOrZero(p.MarginUsed),
				})
			}

			select {
			case ch <- state:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func decOrZero(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func parseLevel(l *wsLevel) (decimal.Decimal, decimal.Decimal, error) {
	price, err := decimal.NewFromString(l.Px)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("parse price %q: %w", l.Px, err)
	}
	size, err := decimal.NewFromString(l.Sz)
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("parse size %q: %w", l.Sz, err)
	}
	return price, size, nil
}

func convertLevels(wls []wsLevel) ([]exchangeclients.PriceLevel, error) {
	levels := make([]exchangeclients.PriceLevel, len(wls))
	for i := range wls {
		price, size, err := parseLevel(&wls[i])
		if err != nil {
			return nil, err
		}
		levels[i] = exchangeclients.PriceLevel{Price: price, Size: size, NumOrders: wls[i].N}
	}
	return levels, nil
}
