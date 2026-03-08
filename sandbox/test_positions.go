//go:build ignore

package main

import (
	"context"
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

	ch, err := client.StreamPositions(ctx)
	if err != nil {
		log.Fatalf("StreamPositions: %v", err)
	}

	for state := range ch {
		fmt.Printf("=== Clearinghouse State ===\n")
		fmt.Printf("Account Value: %s  Margin Used: %s  Notional: %s\n",
			state.MarginSummary.AccountValue,
			state.MarginSummary.TotalMarginUsed,
			state.MarginSummary.TotalNtlPos,
		)
		if len(state.Positions) == 0 {
			fmt.Println("  (no open positions)")
		}
		for _, p := range state.Positions {
			fmt.Printf("  %s  size=%s  entry=%s  mark=%s  liq=%s  upnl=%s  roe=%s\n",
				p.Coin, p.Size, p.EntryPx, p.MarkPx, p.LiquidationPx, p.UnrealizedPnl, p.ReturnOnEquity,
			)
		}
		fmt.Println()
	}
}
