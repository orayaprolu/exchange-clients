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

	// Stream order updates to see what happens
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

	// Get orderbook to find liquidity
	fmt.Println("=== Getting Orderbook ===")
	ob, err := client.GetOrderbook(ctx, "LIGHTER")
	if err != nil {
		log.Fatalf("GetOrderbook: %v", err)
	}

	fmt.Printf("Bids: %d, Asks: %d\n", len(ob.Bids), len(ob.Asks))

	if len(ob.Asks) == 0 {
		log.Fatal("No asks in orderbook")
	}

	// Calculate total available liquidity at best ask
	var totalAvailable decimal.Decimal
	bestAskPx := ob.Asks[0].Price
	for _, ask := range ob.Asks {
		if ask.Price.Equal(bestAskPx) {
			totalAvailable = totalAvailable.Add(ask.Size)
		}
	}

	fmt.Printf("Best ask: %s, Total available: %s\n", bestAskPx, totalAvailable)

	// Place order for 20 (max as requested) - this should get partial fill if total available < 20
	// If available > 20, we'll see full fill
	orderSize := decimal.NewFromInt(20)

	fmt.Printf("\n=== Placing IOC Buy Order ===")
	fmt.Printf("Price: %s (best ask)\n", bestAskPx)
	fmt.Printf("Size: %s\n", orderSize)
	fmt.Printf("Type: IOC (Immediate or Cancel)\n")
	fmt.Printf("Expected: %s filled (if available < 20), otherwise full fill\n", totalAvailable)

	// Place IOC order at best ask price
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

	// Now test with a price far from market to get NO fill
	fmt.Println("\n\n=== Testing IOC with no fill (price far from market) ===")

	if len(ob.Bids) > 0 {
		// Place buy order at price much lower than market (won't fill)
		lowPrice := ob.Bids[0].Price.Mul(decimal.NewFromFloat(0.5))

		fmt.Printf("Placing buy at %s (market is %s)\n", lowPrice, ob.Bids[0].Price)

		orderReq2 := exchangeclients.PlaceOrderRequest{
			Coin:        "LIGHTER",
			IsBuy:       true,
			Price:       lowPrice,
			Size:        decimal.NewFromInt(5),
			OrderType:   exchangeclients.OrderTypeLimit,
			TimeInForce: exchangeclients.TIFIoc,
		}

		resp2, err := client.PlaceOrderWs(ctx, orderReq2)
		if err != nil {
			log.Printf("PlaceOrderWs error: %v", err)
		} else {
			fmt.Printf("Response - Status: %s, OrderID: %s, Error: %s\n",
				resp2.Status, resp2.OrderID, resp2.Error)
		}

		time.Sleep(2 * time.Second)
	}

	fmt.Println("\nDone.")
}
