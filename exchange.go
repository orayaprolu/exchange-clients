package exchangeclients

import "context"

type Streamer interface {
	WsConn
	StreamOrderbook(ctx context.Context, pair string) (<-chan Orderbook, error)
	StreamBBO(ctx context.Context, pair string) (<-chan BBO, error)
	StreamOrderUpdates(ctx context.Context) (<-chan OrderUpdate, error)
}

type WsConn interface {
	Ping(ctx context.Context) error
	Reconnect(ctx context.Context) error
}

type HttpClient interface {
	PlaceOrder(ctx context.Context, req PlaceOrderRequest) (PlaceOrderResponse, error)
	CancelOrder(ctx context.Context, req CancelOrderRequest) (CancelOrderResponse, error)
	ModifyOrder(ctx context.Context, req ModifyOrderRequest) (ModifyOrderResponse, error)
	GetOrderbook(ctx context.Context, pair string) (Orderbook, error)
}

type WebsocketClient interface {
	WsConn
	PlaceOrderWs(ctx context.Context, req PlaceOrderRequest) (PlaceOrderResponse, error)
	CancelOrderWs(ctx context.Context, can CancelOrderRequest) (CancelOrderResponse, error)
	ModifyOrderWs(ctx context.Context, req ModifyOrderRequest) (ModifyOrderResponse, error)
}
