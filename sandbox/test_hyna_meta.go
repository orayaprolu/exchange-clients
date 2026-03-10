//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/orayaprolu/exchange-clients/hyperliquid"
)

func main() {
	_ = godotenv.Load()
	privKey := os.Getenv("HL_PRIVATE_KEY")
	if privKey == "" {
		log.Fatal("set HL_PRIVATE_KEY in .env or as env var")
	}

	client := hyperliquid.New(privKey, "hyna")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get metadata
	meta, err := client.RetrievePerpetualsMetadata(ctx)
	if err != nil {
		log.Fatalf("RetrievePerpetualsMetadata: %v", err)
	}

	fmt.Println("=== PerpDexs Metadata ===")

	// Pretty print
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(meta), &raw); err == nil {
		for i, r := range raw {
			fmt.Printf("%d: %s\n", i, string(r))
		}
	} else {
		fmt.Println(meta)
	}

	// Try to get orderbook for a few different coins
	coins := []string{"LIGTHER", "LIGHTER", "TEST", "BTC"}
	for _, coin := range coins {
		fmt.Printf("\n=== Trying orderbook for %s ===\n", coin)
		ob, err := client.GetOrderbook(ctx, coin)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Printf("Bids: %d, Asks: %d\n", len(ob.Bids), len(ob.Asks))
		if len(ob.Bids) > 0 {
			fmt.Printf("Best bid: %s @ %s\n", ob.Bids[0].Price, ob.Bids[0].Size)
		}
		if len(ob.Asks) > 0 {
			fmt.Printf("Best ask: %s @ %s\n", ob.Asks[0].Price, ob.Asks[0].Size)
		}
	}
}
