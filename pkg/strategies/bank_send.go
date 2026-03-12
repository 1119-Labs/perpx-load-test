package strategies

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// BankSendStrategy handles creation of bank send messages
type BankSendStrategy struct {
	chainID string
	denom   string
}

// NewBankSendStrategy creates a new bank send strategy.
// If receiverPool is non-nil and non-empty, each CreateMsg picks a random receiver from the pool
// (excluding fromAddr) so different senders send to different receivers and txs need not execute sequentially.
// Otherwise sinkAddr is used as the single receiver.
func NewBankSendStrategy(chainID, denom string) (*BankSendStrategy, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}
	if denom == "" {
		return nil, fmt.Errorf("denom cannot be empty")
	}

	return &BankSendStrategy{
		chainID: chainID,
		denom:   denom,
	}, nil
}

// ChainID returns the chain ID
func (s *BankSendStrategy) ChainID() string {
	return s.chainID
}

// Denom returns the denomination
func (s *BankSendStrategy) Denom() string {
	return s.denom
}

// CreateMsgTo creates a bank send message from fromAddr to an explicit toAddr.
func (s *BankSendStrategy) CreateMsgTo(fromAddr, toAddr string) (sdk.Msg, error) {
	amount := sdk.NewCoins(sdk.NewCoin(s.denom, math.NewInt(1)))
	msg := &banktypes.MsgSend{
		FromAddress: fromAddr,
		ToAddress:   toAddr,
		Amount:      amount,
	}
	return msg, nil
}
