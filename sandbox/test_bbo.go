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
	client := hyperliquid.New("", "", "hyna")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := client.StreamBBO(ctx, "BTC")
	if err != nil {
		log.Fatalf("failed to stream BBO: %v", err)
	}

	for bbo := range ch {
		fmt.Printf("[%d] %s  bid: %s @ %s  ask: %s @ %s\n",
			bbo.Time, bbo.Coin,
			bbo.BidPrice.String(), bbo.BidSize.String(),
			bbo.AskPrice.String(), bbo.AskSize.String(),
		)
	}
}
