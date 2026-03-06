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
	// Create client with NO exchange (native Hyperliquid)
	client := hyperliquid.New("", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test 1: Stream Orderbook (L2)
	fmt.Println("=== Test 1: Native Orderbook Stream (BTC) ===")
	obCh, err := client.StreamOrderbook(ctx, "BTC")
	if err != nil {
		log.Fatalf("failed to stream orderbook: %v", err)
	}

	obCount := 0
	for ob := range obCh {
		if obCount < 3 {
			fmt.Printf("[%d] %s Orderbook:\n", ob.Time, ob.Coin)
			fmt.Printf("  Bids: %d levels\n", len(ob.Bids))
			if len(ob.Bids) > 0 {
				fmt.Printf("    Best Bid: %s @ %s\n", ob.Bids[0].Price.String(), ob.Bids[0].Size.String())
			}
			fmt.Printf("  Asks: %d levels\n", len(ob.Asks))
			if len(ob.Asks) > 0 {
				fmt.Printf("    Best Ask: %s @ %s\n", ob.Asks[0].Price.String(), ob.Asks[0].Size.String())
			}
			fmt.Println()
		}
		obCount++
		if obCount >= 5 {
			cancel()
			break
		}
	}

	// Test 2: Stream BBO (Best Bid/Offer)
	fmt.Println("=== Test 2: Native BBO Stream (BTC) ===")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()

	bboCh, err := client.StreamBBO(ctx2, "BTC")
	if err != nil {
		log.Fatalf("failed to stream BBO: %v", err)
	}

	bboCount := 0
	for bbo := range bboCh {
		fmt.Printf("[%d] %s  bid: %s @ %s  ask: %s @ %s\n",
			bbo.Time, bbo.Coin,
			bbo.BidPrice.String(), bbo.BidSize.String(),
			bbo.AskPrice.String(), bbo.AskSize.String(),
		)
		bboCount++
		if bboCount >= 5 {
			cancel2()
			break
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Orderbook updates received: %d\n", obCount)
	fmt.Printf("BBO updates received: %d\n", bboCount)

	if obCount > 0 && bboCount > 0 {
		fmt.Println("✅ Native Hyperliquid stream (BTC) is working correctly!")
	} else {
		fmt.Println("❌ Stream test failed - no updates received")
	}
}
