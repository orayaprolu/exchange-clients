package exchangeclients

import "github.com/shopspring/decimal"

type PriceLevel struct {
	Price     decimal.Decimal
	Size      decimal.Decimal
	NumOrders int
}

type Orderbook struct {
	Coin string
	Bids []PriceLevel
	Asks []PriceLevel
	Time int64
}

type BBO struct {
	Coin     string
	BidPrice decimal.Decimal
	BidSize  decimal.Decimal
	AskPrice decimal.Decimal
	AskSize  decimal.Decimal
	Time     int64
}

type OrderType string

const (
	OrderTypeLimit             OrderType = "limit"
	OrderTypeTriggerStopLoss   OrderType = "stopLoss"
	OrderTypeTriggerTakeProfit OrderType = "takeProfit"
)

type TIF string

const (
	TIFGtc TIF = "Gtc"
	TIFIoc TIF = "Ioc"
	TIFAlo TIF = "Alo"
)

type PlaceOrderRequest struct {
	Coin        string
	IsBuy       bool
	Price       decimal.Decimal
	Size        decimal.Decimal
	ReduceOnly  bool
	OrderType   OrderType
	TimeInForce TIF             // for limit orders
	TriggerPx   decimal.Decimal // for trigger orders
	IsMarket    bool            // for trigger orders: execute as market when triggered
	Cloid       string          // optional client order ID
}

type PlaceOrderResponse struct {
	Status  string
	OrderID string
	Cloid   string
	Error   string
}

type CancelOrderRequest struct {
	Coin    string
	OrderID int64
}

type CancelOrderResponse struct {
	Status string
	Error  string
}

type ModifyOrderRequest struct {
	OrderID     int64
	Coin        string
	IsBuy       bool
	Price       decimal.Decimal
	Size        decimal.Decimal
	ReduceOnly  bool
	OrderType   OrderType
	TimeInForce TIF             // for limit orders
	TriggerPx   decimal.Decimal // for trigger orders
	IsMarket    bool            // for trigger orders
	Cloid       string          // optional client order ID
}

type ModifyOrderResponse struct {
	Status  string
	OrderID string
	Error   string
}

type OrderUpdate struct {
	Coin            string
	Side            string // "Buy" or "Sell"
	LimitPx         decimal.Decimal
	Size            decimal.Decimal
	OrigSize        decimal.Decimal
	OrderID         int64
	Cloid           string
	Timestamp       int64
	Status          string // e.g. "open", "filled", "canceled", "triggered", "rejected", "marginCanceled"
	StatusTimestamp  int64
}
