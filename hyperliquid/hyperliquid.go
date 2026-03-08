package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/orayaprolu/exchange-clients"
	"github.com/shopspring/decimal"
)

// exchange is the HIP-3 deployer prefix (e.g. "flx", "hyna", "xyz").
// Pass empty string for native Hyperliquid perps.
func New(privateKey, exchange string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		httpUrl:     "https://api.hyperliquid.xyz/info",
		exchangeUrl: "https://api.hyperliquid.xyz/exchange",
		wsUrl:       "wss://api.hyperliquid.xyz/ws",
		exchange:    exchange,
		pending:     make(map[int64]chan wsPostResponse),
		bgCtx:       ctx,
		bgCancel:    cancel,
	}

	if privateKey != "" {
		key, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
		if err == nil {
			c.privateKey = key
			c.address = crypto.PubkeyToAddress(key.PublicKey)
		}
	}

	return c
}

// Close cancels all background goroutines (readLoop, pingLoop, orderResponseLoop)
// and releases resources.
func (c *Client) Close() {
	c.bgCancel()
}

func (c *Client) coin(pair string) string {
	if c.exchange == "" {
		return pair
	}
	return c.exchange + ":" + pair
}

// streamCoin returns the coin identifier for a WebSocket subscription.
// Spot pairs (containing "/") are resolved to the Hyperliquid pair name (e.g. "@150").
// Perp pairs use the exchange-prefixed form via coin().
func (c *Client) streamCoin(ctx context.Context, pair string) (string, error) {
	if strings.Contains(pair, "/") {
		return c.resolveSpotCoin(ctx, pair)
	}
	return c.coin(pair), nil
}

// loadSpotMeta fetches the global spot token and universe metadata once
// and populates spotPairMap with "BASE/QUOTE" -> HL pair name entries.
func (c *Client) loadSpotMeta(ctx context.Context) error {
	c.spotPairMapOnce.Do(func() {
		c.spotPairMap = make(map[string]string)

		req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(`{"type":"spotMeta"}`))
		if err != nil {
			c.spotPairMapErr = err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			c.spotPairMapErr = err
			return
		}
		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		if err != nil {
			c.spotPairMapErr = err
			return
		}

		var meta struct {
			Tokens []struct {
				Name  string `json:"name"`
				Index int    `json:"index"`
			} `json:"tokens"`
			Universe []struct {
				Tokens []int  `json:"tokens"`
				Name   string `json:"name"`
			} `json:"universe"`
		}
		if err := json.Unmarshal(b, &meta); err != nil {
			c.spotPairMapErr = err
			return
		}

		tokenNames := make(map[int]string, len(meta.Tokens))
		for _, t := range meta.Tokens {
			tokenNames[t.Index] = t.Name
		}

		for _, u := range meta.Universe {
			if len(u.Tokens) < 2 {
				continue
			}
			base := tokenNames[u.Tokens[0]]
			quote := tokenNames[u.Tokens[1]]
			if base != "" && quote != "" {
				c.spotPairMap[base+"/"+quote] = u.Name
			}
		}
	})
	return c.spotPairMapErr
}

// resolveSpotCoin maps a "BASE/QUOTE" pair to the Hyperliquid spot pair name.
func (c *Client) resolveSpotCoin(ctx context.Context, pair string) (string, error) {
	if err := c.loadSpotMeta(ctx); err != nil {
		return "", fmt.Errorf("load spot meta: %w", err)
	}
	name, ok := c.spotPairMap[pair]
	if !ok {
		return "", fmt.Errorf("unknown spot pair: %s", pair)
	}
	return name, nil
}

func (c *Client) RetrievePerpetualsMetadata(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(`{"type":"perpDexs"}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// loadAssetMap fetches meta endpoints and builds a coin -> asset index map.
// Native perps have offset 0. HIP-3 deployer perps use offset 110000 + i*10000
// where i is the deployer's position in the perpDexs array (excluding the null entry).
func (c *Client) loadAssetMap(ctx context.Context) error {
	var loadErr error
	c.assetMapOnce.Do(func() {
		c.assetMap = make(map[string]int)

		// Fetch native perps universe: {"type":"meta"}
		if err := c.loadDexMeta(ctx, "", 0); err != nil {
			loadErr = fmt.Errorf("load native meta: %w", err)
			return
		}

		// Fetch perpDexs to discover HIP-3 deployers and their offsets
		req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(`{"type":"perpDexs"}`))
		if err != nil {
			return // non-fatal
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return // non-fatal
		}
		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		if err != nil {
			return
		}

		var dexs []json.RawMessage
		if err := json.Unmarshal(b, &dexs); err != nil {
			return
		}

		// First entry is null (native dex, already loaded). Remaining are HIP-3 deployers.
		deployerIdx := 0
		for _, raw := range dexs {
			if string(raw) == "null" {
				continue
			}
			var dex struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &dex); err != nil {
				deployerIdx++
				continue
			}
			offset := 110000 + deployerIdx*10000
			// Fetch this deployer's meta: {"type":"meta","dex":"hyna"}
			_ = c.loadDexMeta(ctx, dex.Name, offset)
			deployerIdx++
		}
	})
	return loadErr
}

// loadDexMeta fetches {"type":"meta","dex":dex} and populates assetMap with the given offset.
func (c *Client) loadDexMeta(ctx context.Context, dex string, offset int) error {
	var payload string
	if dex == "" {
		payload = `{"type":"meta"}`
	} else {
		payload = fmt.Sprintf(`{"type":"meta","dex":"%s"}`, dex)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var meta wsMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return err
	}

	for i, asset := range meta.Universe {
		c.assetMap[asset.Name] = offset + i
	}
	return nil
}

func (c *Client) AssetIndex(ctx context.Context, coin string) (int, error) {
	if err := c.loadAssetMap(ctx); err != nil {
		return 0, err
	}
	idx, ok := c.assetMap[coin]
	if !ok {
		return 0, fmt.Errorf("unknown asset: %s", coin)
	}
	return idx, nil
}

// GetFundingRate fetches the current funding rate for a given coin.
// The coin should be the base symbol (e.g., "BTC", "ETH").
// For HIP-3 deployer exchanges, the coin will be prefixed automatically.
func (c *Client) GetFundingRate(ctx context.Context, coin string) (exchangeclients.FundingRate, error) {
	var result exchangeclients.FundingRate

	// Get the full coin name with exchange prefix if applicable
	fullCoin := c.coin(coin)

	// Build the request payload
	var payload string
	if c.exchange == "" {
		payload = `{"type":"metaAndAssetCtxs"}`
	} else {
		payload = fmt.Sprintf(`{"type":"metaAndAssetCtxs","dex":"%s"}`, c.exchange)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.httpUrl, strings.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, err
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return result, err
	}

	// Parse response: [meta, [assetCtxs]]
	var response []json.RawMessage
	if err := json.Unmarshal(b, &response); err != nil {
		return result, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(response) < 2 {
		return result, fmt.Errorf("invalid response format")
	}

	// Parse meta to get universe order
	var meta struct {
		Universe []wsAssetMeta `json:"universe"`
	}
	if err := json.Unmarshal(response[0], &meta); err != nil {
		return result, fmt.Errorf("unmarshal meta: %w", err)
	}

	// Parse asset contexts
	var assetCtxs []AssetContext
	if err := json.Unmarshal(response[1], &assetCtxs); err != nil {
		return result, fmt.Errorf("unmarshal asset ctxs: %w", err)
	}

	// Find the coin in the universe and get its funding rate
	for i, asset := range meta.Universe {
		if asset.Name == fullCoin {
			if i < len(assetCtxs) {
				ctx := assetCtxs[i]
				result.Coin = coin
				result.FundingRate, _ = decimal.NewFromString(ctx.Funding)
				result.MarkPrice, _ = decimal.NewFromString(ctx.MarkPx)
				result.OraclePrice, _ = decimal.NewFromString(ctx.OraclePx)
				result.Premium, _ = decimal.NewFromString(ctx.Premium)
				result.OpenInterest, _ = decimal.NewFromString(ctx.OpenInterest)
				result.DayVolume, _ = decimal.NewFromString(ctx.DayNtlVlm)
				result.PrevDayPrice, _ = decimal.NewFromString(ctx.PrevDayPx)
				return result, nil
			}
			return result, fmt.Errorf("asset context not found for %s", fullCoin)
		}
	}

	return result, fmt.Errorf("coin not found: %s", fullCoin)
}
