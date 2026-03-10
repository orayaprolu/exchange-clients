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

	// Get orderbook to find a good price for partial fill
	fmt.Println("=== Getting Orderbook ===")
	ob, err := client.GetOrderbook(ctx, "LIGHTER")
	if err != nil {
		log.Fatalf("GetOrderbook: %v", err)
	}

	fmt.Printf("Bids: %d, Asks: %d\n", len(ob.Bids), len(ob.Asks))

	if len(ob.Bids) == 0 && len(ob.Asks) == 0 {
		log.Fatal("Orderbook is empty - no liquidity")
	}

	if len(ob.Asks) == 0 {
		log.Fatal("No asks in orderbook - cannot test buy order")
	}

	// Find best ask price
	bestAsk := ob.Asks[0]
	fmt.Printf("Best ask: %s @ %s\n", bestAsk.Price, bestAsk.Size)

	// For partial fill test, we want to place an order larger than available liquidity
	// but not too large (max 20 as requested)
	// Best ask has 339 available, so let's try to buy 500 (will get partial fill)
	orderSize := decimal.NewFromFloat(500)
	maxSize := decimal.NewFromInt(20)
	if orderSize.GreaterThan(maxSize) {
		orderSize = maxSize
	}

	fmt.Printf("\n=== Placing IOC Buy Order ===")
	fmt.Printf("Price: %s (best ask)\n", bestAsk.Price)
	fmt.Printf("Size: %s\n", orderSize)
	fmt.Printf("Type: IOC\n")

	// Place IOC order at best ask price (should fill immediately or partially)
	orderReq := exchangeclients.PlaceOrderRequest{
		Coin:        "LIGHTER",
		IsBuy:       true,
		Price:       bestAsk.Price,
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
	fmt.Printf("Error: %s\n", resp.Error)

	// Wait to see if we get any order updates
	fmt.Println("\n=== Waiting for order updates (5s) ===")
	time.Sleep(5 * time.Second)

	fmt.Println("\nDone.")
}
