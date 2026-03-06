//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/orayaprolu/exchange-clients/hyperliquid"
)

func main() {
	// Test 1: main exchange - PURR/USDC (canonical spot pair)
	fmt.Println("=== Main exchange: PURR/USDC ===")
	testSpot(hyperliquid.New("", ""), "PURR/USDC", 15*time.Second)

	// Test 2: HYNA exchange - USDE/USDC
	fmt.Println("\n=== HYNA exchange: USDE/USDC ===")
	testSpot(hyperliquid.New("", "hyna"), "USDE/USDC", 15*time.Second)
}

func testSpot(client *hyperliquid.Client, pair string, dur time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()

	// Note: StreamBBO is not supported for spot pairs by Hyperliquid.
	_, err := client.StreamBBO(ctx, pair)
	if err != nil {
		fmt.Printf("StreamBBO correctly rejected spot pair: %v\n", err)
	}

	obCh, err := client.StreamOrderbook(ctx, pair)
	if err != nil {
		log.Printf("StreamOrderbook failed for %s: %v", pair, err)
		return
	}

	fmt.Printf("Streaming %s BBO and L2 orderbook for %v...\n", pair, dur)

	obCount := 0
	for {
		select {
		case ob, ok := <-obCh:
			if !ok {
				fmt.Printf("Orderbook channel closed (received %d updates)\n", obCount)
				return
			}
			obCount++
			topBid, topAsk := "none", "none"
			if len(ob.Bids) > 0 {
				topBid = fmt.Sprintf("%s @ %s", ob.Bids[0].Price.String(), ob.Bids[0].Size.String())
			}
			if len(ob.Asks) > 0 {
				topAsk = fmt.Sprintf("%s @ %s", ob.Asks[0].Price.String(), ob.Asks[0].Size.String())
			}
			fmt.Printf("[L2  ] [%d] %s  top bid: %s  top ask: %s  (levels: %d/%d)\n",
				ob.Time, ob.Coin, topBid, topAsk, len(ob.Bids), len(ob.Asks),
			)
		case <-ctx.Done():
			fmt.Printf("done (L2: %d updates)\n", obCount)
			return
		}
	}
}
