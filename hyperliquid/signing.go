package hyperliquid

import (
	"crypto/ecdsa"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/vmihailenco/msgpack/v5"
)

// signAction produces an EIP-712 signature for a Hyperliquid websocket action.
// Steps: msgpack-encode action → build connection ID → sign phantom agent.
func signAction(key *ecdsa.PrivateKey, action any, nonce int64, vaultAddress *common.Address) (wsSignature, error) {
	packed, err := msgpack.Marshal(action)
	if err != nil {
		return wsSignature{}, fmt.Errorf("msgpack marshal: %w", err)
	}

	// hashInput = msgpackBytes + nonce(8-byte BE) + vaultFlag(1 byte) [+ vault(20 bytes)]
	nonceBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(nonceBuf, uint64(nonce))

	var hashInput []byte
	hashInput = append(hashInput, packed...)
	hashInput = append(hashInput, nonceBuf...)

	if vaultAddress != nil {
		hashInput = append(hashInput, 1)
		hashInput = append(hashInput, vaultAddress.Bytes()...)
	} else {
		hashInput = append(hashInput, 0)
	}

	connectionID := crypto.Keccak256Hash(hashInput)

	return eip712Sign(key, "a", connectionID)
}

// eip712Sign signs a phantom agent struct using EIP-712.
// Domain: {name: "Exchange", version: "1", chainId: 1337, verifyingContract: 0x0...0}
// Type: Agent [{source: string}, {connectionId: bytes32}]
func eip712Sign(key *ecdsa.PrivateKey, source string, connectionID common.Hash) (wsSignature, error) {
	// EIP712Domain type hash
	domainTypeHash := crypto.Keccak256Hash([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

	nameHash := crypto.Keccak256Hash([]byte("Exchange"))
	versionHash := crypto.Keccak256Hash([]byte("1"))
	chainID := common.LeftPadBytes(big.NewInt(1337).Bytes(), 32)
	verifyingContract := common.LeftPadBytes(common.Address{}.Bytes(), 32)

	domainSeparator := crypto.Keccak256Hash(
		append(append(append(append(
			domainTypeHash.Bytes(),
			nameHash.Bytes()...),
			versionHash.Bytes()...),
			chainID...),
			verifyingContract...),
	)

	// Agent type hash
	agentTypeHash := crypto.Keccak256Hash([]byte("Agent(string source,bytes32 connectionId)"))
	sourceHash := crypto.Keccak256Hash([]byte(source))

	structHash := crypto.Keccak256Hash(
		append(append(
			agentTypeHash.Bytes(),
			sourceHash.Bytes()...),
			connectionID.Bytes()...),
	)

	// Final digest: \x19\x01 + domainSeparator + structHash
	digest := crypto.Keccak256Hash(
		append(append(
			[]byte{0x19, 0x01},
			domainSeparator.Bytes()...),
			structHash.Bytes()...),
	)

	sig, err := crypto.Sign(digest.Bytes(), key)
	if err != nil {
		return wsSignature{}, fmt.Errorf("ecdsa sign: %w", err)
	}

	// sig is [R(32) || S(32) || V(1)], V is 0 or 1, needs +27
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	v := int(sig[64]) + 27

	return wsSignature{
		R: fmt.Sprintf("0x%064x", r),
		S: fmt.Sprintf("0x%064x", s),
		V: v,
	}, nil
}
