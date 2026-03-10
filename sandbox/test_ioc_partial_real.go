//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	exchangeclients "github.com/orayaprolu/exchange-clients"
	"github.com/orayaprolu/exchange-clients/hyperliquid"
	"github.com/shopspring/decimal"
)

func main() {
	_ = godotenv.Load()
	privKey := os.Getenv("HL_PRIVATE_KEY")
	if privKey == "" {
		log.Fatal("set HL_PRIVATE_KEY in .env or as env var")
	}

	client := hyperliquid.New(privKey, "hyna")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Stream order updates
	updateCh, err := client.StreamOrderUpdates(ctx)
	if err != nil {
		log.Fatalf("StreamOrderUpdates: %v", err)
	}
	go func() {
		for u := range updateCh {
			fmt.Printf("[ORDER UPDATE] oid=%d coin=%s side=%s status=%s px=%s sz=%s origSz=%s\n",
				u.OrderID, u.Coin, u.Side, u.Status, u.LimitPx, u.Size, u.OrigSize)
		}
	}()

	time.Sleep(1 * time.Second)

	// Get orderbook
	fmt.Println("=== Getting Orderbook ===")
	ob, err := client.GetOrderbook(ctx, "LIGHTER")
	if err != nil {
		log.Fatalf("GetOrderbook: %v", err)
	}

	fmt.Printf("Bids: %d, Asks: %d\n", len(ob.Bids), len(ob.Asks))

	if len(ob.Asks) == 0 {
		log.Fatal("No asks in orderbook")
	}

	// Calculate total liquidity in top 3 price levels
	var totalLiquidity decimal.Decimal
	levelsToCheck := 3
	if len(ob.Asks) < levelsToCheck {
		levelsToCheck = len(ob.Asks)
	}

	for i := 0; i < levelsToCheck; i++ {
		fmt.Printf("Ask level %d: %s @ %s\n", i+1, ob.Asks[i].Price, ob.Asks[i].Size)
		totalLiquidity = totalLiquidity.Add(ob.Asks[i].Size)
	}

	fmt.Printf("\nTotal liquidity in top %d levels: %s\n", levelsToCheck, totalLiquidity)

	// Place order for 30 (larger than typical liquidity to force partial fill)
	orderSize := decimal.NewFromInt(30)
	bestAskPx := ob.Asks[0].Price

	fmt.Printf("\n=== Placing IOC Buy Order ===")
	fmt.Printf("Price: %s (best ask)\n", bestAskPx)
	fmt.Printf("Size: %s\n", orderSize)
	fmt.Printf("Type: IOC (Immediate or Cancel)\n")
	fmt.Printf("Expected: Should get partial fill if liquidity < 30\n")

	orderReq := exchangeclients.PlaceOrderRequest{
		Coin:        "LIGHTER",
		IsBuy:       true,
		Price:       bestAskPx,
		Size:        orderSize,
		OrderType:   exchangeclients.OrderTypeLimit,
		TimeInForce: exchangeclients.TIFIoc,
	}

	resp, err := client.PlaceOrderWs(ctx, orderReq)
	if err != nil {
		log.Fatalf("PlaceOrderWs error: %v", err)
	}

	fmt.Printf("\n=== Order Response ===")
	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("OrderID: %s\n", resp.OrderID)
	fmt.Printf("Cloid: %s\n", resp.Cloid)
	if resp.Error != "" {
		fmt.Printf("Error: %s\n", resp.Error)
	}

	// Wait to see order updates
	fmt.Println("\n=== Waiting for order updates (5s) ===")
	time.Sleep(5 * time.Second)

	fmt.Println("\nDone.")
}
