//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync/atomic"
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

	client := hyperliquid.New("", privKey, "hyna")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start order update stream — capture latest OID for cancel-after-modify
	var latestOID atomic.Int64
	updateCh, err := client.StreamOrderUpdates(ctx)
	if err != nil {
		log.Fatalf("StreamOrderUpdates: %v", err)
	}
	go func() {
		for u := range updateCh {
			fmt.Printf("  [UPDATE] oid=%d status=%s px=%s sz=%s\n", u.OrderID, u.Status, u.LimitPx, u.Size)
			if u.Status == "open" {
				latestOID.Store(u.OrderID)
			}
		}
	}()
	time.Sleep(1 * time.Second)

	orderReq := exchangeclients.PlaceOrderRequest{
		Coin:        "BTC",
		IsBuy:       true,
		Price:       decimal.NewFromInt(10000),
		Size:        decimal.NewFromFloat(0.001),
		OrderType:   exchangeclients.OrderTypeLimit,
		TimeInForce: exchangeclients.TIFGtc,
	}

	// --- GetOrderbook (HTTP) ---
	fmt.Println("=== GetOrderbook (HTTP) ===")
	ob, err := client.GetOrderbook(ctx, "BTC")
	if err != nil {
		log.Fatalf("GetOrderbook: %v", err)
	}
	fmt.Printf("Coin: %s  Time: %d  Bids: %d  Asks: %d\n", ob.Coin, ob.Time, len(ob.Bids), len(ob.Asks))
	if len(ob.Bids) > 0 {
		fmt.Printf("  Best bid: %s @ %s\n", ob.Bids[0].Price, ob.Bids[0].Size)
	}
	if len(ob.Asks) > 0 {
		fmt.Printf("  Best ask: %s @ %s\n", ob.Asks[0].Price, ob.Asks[0].Size)
	}

	// --- WS place + modify + cancel ---
	fmt.Println("\n=== PlaceOrderWs ===")
	resp, err := client.PlaceOrderWs(ctx, orderReq)
	if err != nil {
		log.Fatalf("PlaceOrderWs: %v", err)
	}
	fmt.Printf("Status: %s  OrderID: %s\n", resp.Status, resp.OrderID)
	oid, _ := strconv.ParseInt(resp.OrderID, 10, 64)
	time.Sleep(1 * time.Second)

	fmt.Println("\n=== ModifyOrderWs (price 10000 -> 10001) ===")
	modResp, err := client.ModifyOrderWs(ctx, exchangeclients.ModifyOrderRequest{
		OrderID:     oid,
		Coin:        "BTC",
		IsBuy:       true,
		Price:       decimal.NewFromInt(10001),
		Size:        decimal.NewFromFloat(0.001),
		OrderType:   exchangeclients.OrderTypeLimit,
		TimeInForce: exchangeclients.TIFGtc,
	})
	if err != nil {
		log.Fatalf("ModifyOrderWs: %v", err)
	}
	fmt.Printf("Status: %s  OrderID: %s  Error: %s\n", modResp.Status, modResp.OrderID, modResp.Error)
	time.Sleep(1 * time.Second)

	// Cancel the modified order — modify creates a new OID visible via update stream
	modOid := latestOID.Load()
	if modOid == 0 {
		modOid = oid // fallback
	}
	fmt.Printf("\n=== CancelOrderWs (oid=%d) ===\n", modOid)
	cancelResp, err := client.CancelOrderWs(ctx, exchangeclients.CancelOrderRequest{Coin: "BTC", OrderID: modOid})
	if err != nil {
		log.Fatalf("CancelOrderWs: %v", err)
	}
	fmt.Printf("Status: %s  Error: %s\n", cancelResp.Status, cancelResp.Error)
	time.Sleep(1 * time.Second)

	// --- HTTP place + modify + cancel ---
	fmt.Println("\n=== PlaceOrder (HTTP) ===")
	resp2, err := client.PlaceOrder(ctx, orderReq)
	if err != nil {
		log.Fatalf("PlaceOrder: %v", err)
	}
	fmt.Printf("Status: %s  OrderID: %s\n", resp2.Status, resp2.OrderID)
	oid2, _ := strconv.ParseInt(resp2.OrderID, 10, 64)
	time.Sleep(1 * time.Second)

	fmt.Println("\n=== ModifyOrder (HTTP, price 10000 -> 10002) ===")
	modResp2, err := client.ModifyOrder(ctx, exchangeclients.ModifyOrderRequest{
		OrderID:     oid2,
		Coin:        "BTC",
		IsBuy:       true,
		Price:       decimal.NewFromInt(10002),
		Size:        decimal.NewFromFloat(0.001),
		OrderType:   exchangeclients.OrderTypeLimit,
		TimeInForce: exchangeclients.TIFGtc,
	})
	if err != nil {
		log.Fatalf("ModifyOrder: %v", err)
	}
	fmt.Printf("Status: %s  OrderID: %s  Error: %s\n", modResp2.Status, modResp2.OrderID, modResp2.Error)
	time.Sleep(1 * time.Second)

	// Cancel — use latest OID from update stream (modify creates new OID)
	modOid2 := latestOID.Load()
	if modOid2 == 0 {
		modOid2 = oid2 // fallback
	}
	fmt.Printf("\n=== CancelOrder (HTTP, oid=%d) ===\n", modOid2)
	cancelResp2, err := client.CancelOrder(ctx, exchangeclients.CancelOrderRequest{Coin: "BTC", OrderID: modOid2})
	if err != nil {
		log.Fatalf("CancelOrder: %v", err)
	}
	fmt.Printf("Status: %s  Error: %s\n", cancelResp2.Status, cancelResp2.Error)
	time.Sleep(1 * time.Second)

	fmt.Println("\nDone.")
}
