//go:build ignore

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/orayaprolu/exchange-clients/hyperliquid"
)

func main() {
	client := hyperliquid.New("", "")

	res, err := client.RetrievePerpetualsMetadata(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(res), "", "  "); err != nil {
		log.Fatal(err)
	}
	fmt.Println(pretty.String())
}
