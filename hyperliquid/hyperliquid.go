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
	c := &Client{
		httpUrl:     "https://api.hyperliquid.xyz/info",
		exchangeUrl: "https://api.hyperliquid.xyz/exchange",
		wsUrl:       "wss://api.hyperliquid.xyz/ws",
		exchange:    exchange,
		pending:     make(map[int64]chan wsPostResponse),
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

func (c *Client) coin(pair string) string {
	if c.exchange == "" {
		return pair
	}
	return c.exchange + ":" + pair
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
