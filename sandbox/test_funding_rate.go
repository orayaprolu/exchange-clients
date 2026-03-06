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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test native Hyperliquid BTC funding rate
	fmt.Println("=== Testing Native Hyperliquid BTC ===")
	nativeClient := hyperliquid.New("", "")
	nativeFunding, err := nativeClient.GetFundingRate(ctx, "BTC")
	if err != nil {
		log.Fatalf("Failed to get native BTC funding rate: %v", err)
	}
	fmt.Printf("Coin: %s\n", nativeFunding.Coin)
	fmt.Printf("Funding Rate: %s\n", nativeFunding.FundingRate)
	fmt.Printf("Mark Price: %s\n", nativeFunding.MarkPrice)
	fmt.Printf("Oracle Price: %s\n", nativeFunding.OraclePrice)
	fmt.Printf("Premium: %s\n", nativeFunding.Premium)
	fmt.Printf("Open Interest: %s\n", nativeFunding.OpenInterest)
	fmt.Printf("Day Volume: %s\n", nativeFunding.DayVolume)
	fmt.Printf("Prev Day Price: %s\n", nativeFunding.PrevDayPrice)

	// Test Hyna exchange BTC funding rate
	fmt.Println("\n=== Testing Hyna Exchange BTC ===")
	hyenaClient := hyperliquid.New("", "hyna")
	hyenaFunding, err := hyenaClient.GetFundingRate(ctx, "BTC")
	if err != nil {
		log.Fatalf("Failed to get hyena BTC funding rate: %v", err)
	}
	fmt.Printf("Coin: %s\n", hyenaFunding.Coin)
	fmt.Printf("Funding Rate: %s\n", hyenaFunding.FundingRate)
	fmt.Printf("Mark Price: %s\n", hyenaFunding.MarkPrice)
	fmt.Printf("Oracle Price: %s\n", hyenaFunding.OraclePrice)
	fmt.Printf("Premium: %s\n", hyenaFunding.Premium)
	fmt.Printf("Open Interest: %s\n", hyenaFunding.OpenInterest)
	fmt.Printf("Day Volume: %s\n", hyenaFunding.DayVolume)
	fmt.Printf("Prev Day Price: %s\n", hyenaFunding.PrevDayPrice)

	fmt.Println("\n=== Tests completed successfully ===")
}
