package hyperliquid

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/websocket"
)

type Client struct {
	httpUrl     string
	exchangeUrl string
	wsUrl       string
	exchange    string // HIP-3 deployer prefix (e.g. "flx", "hyna"). Empty for native HL perps.
	mu          sync.Mutex
	conns       []*wsConn

	// Background context for long-lived goroutines (readLoop, pingLoop, orderResponseLoop).
	bgCtx    context.Context
	bgCancel context.CancelFunc

	// Order placement fields
	privateKey *ecdsa.PrivateKey
	address    common.Address

	assetMap     map[string]int
	assetMapOnce sync.Once

	spotPairMap     map[string]string // "BASE/QUOTE" -> HL pair name (e.g. "@150")
	spotPairMapOnce sync.Once
	spotPairMapErr  error

	orderConnOnce sync.Once
	orderConn     *wsConn
	orderConnErr  error
	nextID        atomic.Int64
	pendingMu     sync.Mutex
	pending       map[int64]chan wsPostResponse
}

type wsConn struct {
	wsUrl     string
	subType   string
	subParams map[string]string   // extra subscription params (e.g. "coin" or "user")
	subs      []map[string]string // multiple subscriptions (for mux connections)
	conn      *websocket.Conn
	mu        sync.Mutex
	msgCh     chan []byte
}

// Wire types for websocket JSON messages.

type wsSubscription struct {
	Method       string            `json:"method"`
	Subscription map[string]string `json:"subscription"`
}

type wsLevel struct {
	Px string `json:"px"`
	Sz string `json:"sz"`
	N  int    `json:"n"`
}

type wsBookMessage struct {
	Channel string `json:"channel"`
	Data    struct {
		Coin   string       `json:"coin"`
		Levels [2][]wsLevel `json:"levels"`
		Time   int64        `json:"time"`
	} `json:"data"`
}

type wsBboMessage struct {
	Channel string `json:"channel"`
	Data    struct {
		Coin string      `json:"coin"`
		Time int64       `json:"time"`
		Bbo  [2]*wsLevel `json:"bbo"`
	} `json:"data"`
}

// Wire types for order placement over websocket.

type wsPostRequest struct {
	Method  string          `json:"method"`
	ID      int64           `json:"id"`
	Request wsActionPayload `json:"request"`
}

type wsActionPayload struct {
	Type    string         `json:"type"`
	Payload wsSignedAction `json:"payload"`
}

type wsSignedAction struct {
	Action       any         `json:"action"`
	Nonce        int64       `json:"nonce"`
	Signature    wsSignature `json:"signature"`
	VaultAddress *string     `json:"vaultAddress"`
}

type wsSignature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

type wsOrderAction struct {
	Type     string    `json:"type" msgpack:"type"`
	Orders   []wsOrder `json:"orders" msgpack:"orders"`
	Grouping string    `json:"grouping" msgpack:"grouping"`
}

type wsOrder struct {
	A int         `json:"a" msgpack:"a"`
	B bool        `json:"b" msgpack:"b"`
	P string      `json:"p" msgpack:"p"`
	S string      `json:"s" msgpack:"s"`
	R bool        `json:"r" msgpack:"r"`
	T wsOrderType `json:"t" msgpack:"t"`
	C string      `json:"c,omitempty" msgpack:"c,omitempty"`
}

type wsOrderType struct {
	Limit   *wsLimitType   `json:"limit,omitempty" msgpack:"limit,omitempty"`
	Trigger *wsTriggerType `json:"trigger,omitempty" msgpack:"trigger,omitempty"`
}

type wsLimitType struct {
	TIF string `json:"tif" msgpack:"tif"`
}

type wsTriggerType struct {
	TriggerPx string `json:"triggerPx" msgpack:"triggerPx"`
	IsMarket  bool   `json:"isMarket" msgpack:"isMarket"`
	TPSL      string `json:"tpsl" msgpack:"tpsl"`
}

type wsCancelAction struct {
	Type    string     `json:"type" msgpack:"type"`
	Cancels []wsCancel `json:"cancels" msgpack:"cancels"`
}

type wsCancel struct {
	A int    `json:"a" msgpack:"a"`
	O uint64 `json:"o" msgpack:"o"`
}

type wsModifyAction struct {
	Type  string  `json:"type" msgpack:"type"`
	OID   uint64  `json:"oid" msgpack:"oid"`
	Order wsOrder `json:"order" msgpack:"order"`
}

// HTTP exchange endpoint request/response types.

type httpExchangeRequest struct {
	Action       any         `json:"action"`
	Nonce        int64       `json:"nonce"`
	Signature    wsSignature `json:"signature"`
	VaultAddress *string     `json:"vaultAddress"`
}

type httpExchangeResponse struct {
	Status   string          `json:"status"`
	Response json.RawMessage `json:"response"`
}

// Wire type for order response from websocket.

type wsPostResponse struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

type wsPostResponseData struct {
	ID       json.Number `json:"id"`
	Response struct {
		Type    string `json:"type"`
		Payload struct {
			Status   string          `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"payload"`
	} `json:"response"`
}

type wsPayloadResponse struct {
	Type string `json:"type"`
	Data struct {
		Statuses []json.RawMessage `json:"statuses"`
	} `json:"data"`
}

type wsOrderStatus struct {
	Resting *wsOrderResting `json:"resting,omitempty"`
	Filled  *wsOrderFilled  `json:"filled,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type wsOrderResting struct {
	OID   int64  `json:"oid"`
	Cloid string `json:"cloid,omitempty"`
}

type wsOrderFilled struct {
	OID     int64  `json:"oid"`
	TotalSz string `json:"totalSz"`
	AvgPx   string `json:"avgPx"`
	Cloid   string `json:"cloid,omitempty"`
}

// Wire type for order update subscription.

type wsOrderUpdateMessage struct {
	Channel string          `json:"channel"`
	Data    []wsOrderUpdate `json:"data"`
}

type wsOrderUpdate struct {
	Order           wsBasicOrder `json:"order"`
	Status          string       `json:"status"`
	StatusTimestamp int64        `json:"statusTimestamp"`
}

type wsBasicOrder struct {
	Coin      string `json:"coin"`
	Side      string `json:"side"`
	LimitPx   string `json:"limitPx"`
	Sz        string `json:"sz"`
	OID       int64  `json:"oid"`
	Timestamp int64  `json:"timestamp"`
	OrigSz    string `json:"origSz"`
	Cloid     string `json:"cloid,omitempty"`
}

// Wire types for clearinghouse state subscription.

type wsClearinghouseMessage struct {
	Channel string                  `json:"channel"`
	Data    wsClearinghouseEnvelope `json:"data"`
}

type wsClearinghouseEnvelope struct {
	ClearinghouseState wsClearinghouseState `json:"clearinghouseState"`
}

type wsClearinghouseState struct {
	AssetPositions []wsAssetPosition `json:"assetPositions"`
	MarginSummary  wsMarginSummary   `json:"marginSummary"`
}

type wsAssetPosition struct {
	Type     string     `json:"type"`
	Position wsPosition `json:"position"`
}

type wsPosition struct {
	Coin           string `json:"coin"`
	Szi            string `json:"szi"`
	EntryPx        string `json:"entryPx"`
	MarkPx         string `json:"markPx"`
	LiquidationPx  string `json:"liquidationPx"`
	UnrealizedPnl  string `json:"unrealizedPnl"`
	ReturnOnEquity string `json:"returnOnEquity"`
	MarginUsed     string `json:"marginUsed"`
}

type wsMarginSummary struct {
	AccountValue    string `json:"accountValue"`
	TotalNtlPos     string `json:"totalNtlPos"`
	TotalRawUsd     string `json:"totalRawUsd"`
	TotalMarginUsed string `json:"totalMarginUsed"`
}

type wsTradeMessage struct {
	Channel string    `json:"channel"`
	Data    []wsTrade `json:"data"`
}

type wsTrade struct {
	Coin  string    `json:"coin"`
	Side  string    `json:"side"`
	Px    string    `json:"px"`
	Sz    string    `json:"sz"`
	Hash  string    `json:"hash"`
	Time  int64     `json:"time"`
	TID   int64     `json:"tid"`
	Users [2]string `json:"users"`
}

// Meta endpoint types for asset index mapping.

type wsMeta struct {
	Universe []wsAssetMeta `json:"universe"`
}

type wsAssetMeta struct {
	Name string `json:"name"`
}

// Funding rate types for metaAndAssetCtxs endpoint

type AssetContext struct {
	DayNtlVlm    string   `json:"dayNtlVlm"`
	Funding      string   `json:"funding"`
	ImpactPxs    []string `json:"impactPxs"`
	MarkPx       string   `json:"markPx"`
	MidPx        string   `json:"midPx"`
	OpenInterest string   `json:"openInterest"`
	OraclePx     string   `json:"oraclePx"`
	Premium      string   `json:"premium"`
	PrevDayPx    string   `json:"prevDayPx"`
	DayBaseVlm   string   `json:"dayBaseVlm,omitempty"`
}

type MetaAndAssetCtxsResponse struct {
	Meta struct {
		Universe []wsAssetMeta `json:"universe"`
	} `json:"universe"`
	AssetCtxs []AssetContext `json:"assetCtxs"`
}
