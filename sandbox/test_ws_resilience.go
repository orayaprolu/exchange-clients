//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
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
		log.Fatal("set HL_PRIVATE_KEY in .env")
	}

	client := hyperliquid.New("", privKey, "hyna")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start order update stream
	updateCh, err := client.StreamOrderUpdates(ctx)
	if err != nil {
		log.Fatalf("StreamOrderUpdates: %v", err)
	}

	// Track updates by order ID with timestamps
	var mu sync.Mutex
	updates := make(map[int64]time.Time) // oid -> when update received
	orderSent := make(map[int64]time.Time) // oid -> when order was sent

	go func() {
		for u := range updateCh {
			mu.Lock()
			updates[u.OrderID] = time.Now()
			sent, hasSent := orderSent[u.OrderID]
			mu.Unlock()

			latency := ""
			if hasSent {
				latency = fmt.Sprintf(" (latency: %s)", time.Since(sent).Round(time.Microsecond))
			}
			fmt.Printf("  [UPDATE] oid=%d status=%-10s%s\n", u.OrderID, u.Status, latency)
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

	// --- Test 1: Latency across multiple place/cancel cycles ---
	fmt.Println("=== Test 1: Latency (5 place/cancel cycles) ===")
	var latencies []time.Duration

	for i := range 5 {
		// Place
		sendTime := time.Now()
		resp, err := client.PlaceOrderWs(ctx, orderReq)
		if err != nil {
			log.Fatalf("cycle %d place: %v", i+1, err)
		}
		oid, _ := strconv.ParseInt(resp.OrderID, 10, 64)

		mu.Lock()
		orderSent[oid] = sendTime
		mu.Unlock()

		// Wait for the open update
		deadline := time.After(5 * time.Second)
		for {
			mu.Lock()
			_, got := updates[oid]
			mu.Unlock()
			if got {
				mu.Lock()
				latencies = append(latencies, updates[oid].Sub(sendTime))
				mu.Unlock()
				break
			}
			select {
			case <-deadline:
				fmt.Printf("  cycle %d: timed out waiting for update\n", i+1)
				goto next
			case <-time.After(5 * time.Millisecond):
			}
		}

	next:
		// Cancel
		cancelTime := time.Now()
		cancelOid := oid
		_, err = client.CancelOrderWs(ctx, exchangeclients.CancelOrderRequest{
			Coin: "BTC", OrderID: cancelOid,
		})
		if err != nil {
			log.Fatalf("cycle %d cancel: %v", i+1, err)
		}

		// Wait for cancel update (different timestamp than open)
		cancelDeadline := time.After(5 * time.Second)
		for {
			mu.Lock()
			recvTime, got := updates[cancelOid]
			mu.Unlock()
			if got && recvTime.After(cancelTime) {
				mu.Lock()
				latencies = append(latencies, recvTime.Sub(cancelTime))
				mu.Unlock()
				break
			}
			select {
			case <-cancelDeadline:
				fmt.Printf("  cycle %d: timed out waiting for cancel update\n", i+1)
				break
			case <-time.After(5 * time.Millisecond):
				continue
			}
			break
		}

		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println("\n=== Latency Summary ===")
	if len(latencies) > 0 {
		var total time.Duration
		minL, maxL := latencies[0], latencies[0]
		for _, l := range latencies {
			total += l
			if l < minL {
				minL = l
			}
			if l > maxL {
				maxL = l
			}
		}
		fmt.Printf("  samples: %d\n", len(latencies))
		fmt.Printf("  min:     %s\n", minL.Round(time.Microsecond))
		fmt.Printf("  max:     %s\n", maxL.Round(time.Microsecond))
		fmt.Printf("  avg:     %s\n", (total / time.Duration(len(latencies))).Round(time.Microsecond))
	}

	// --- Test 2: Reconnection resilience ---
	fmt.Println("\n=== Test 2: Reconnect resilience ===")
	fmt.Println("  Forcing reconnect on all connections...")
	if err := client.Reconnect(ctx); err != nil {
		log.Fatalf("Reconnect: %v", err)
	}
	fmt.Println("  Reconnected. Waiting for subscription to re-establish...")
	time.Sleep(2 * time.Second)

	fmt.Println("  Placing order after reconnect...")
	resp, err := client.PlaceOrderWs(ctx, orderReq)
	if err != nil {
		log.Fatalf("post-reconnect place: %v", err)
	}
	oid, _ := strconv.ParseInt(resp.OrderID, 10, 64)
	fmt.Printf("  Placed oid=%d status=%s\n", oid, resp.Status)

	mu.Lock()
	orderSent[oid] = time.Now()
	mu.Unlock()

	// Wait for update after reconnect
	deadline := time.After(5 * time.Second)
	gotUpdate := false
	for {
		mu.Lock()
		_, got := updates[oid]
		mu.Unlock()
		if got {
			gotUpdate = true
			break
		}
		select {
		case <-deadline:
			break
		case <-time.After(10 * time.Millisecond):
			continue
		}
		break
	}

	if gotUpdate {
		fmt.Println("  Order update received after reconnect!")
	} else {
		fmt.Println("  WARNING: No update after reconnect (stream may need re-subscribe)")
	}

	// Cleanup
	if oid > 0 {
		client.CancelOrderWs(ctx, exchangeclients.CancelOrderRequest{Coin: "BTC", OrderID: oid})
	}

	time.Sleep(1 * time.Second)
	fmt.Println("\nDone.")
}
