package strategies

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// BankSendStrategy handles creation of bank send messages
type BankSendStrategy struct {
	chainID  string
	denom    string
	sinkAddr string
}

// NewBankSendStrategy creates a new bank send strategy
func NewBankSendStrategy(chainID, denom, sinkAddr string) (*BankSendStrategy, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}
	if denom == "" {
		return nil, fmt.Errorf("denom cannot be empty")
	}
	if sinkAddr == "" {
		return nil, fmt.Errorf("sink address cannot be empty")
	}

	// Validate sink address
	_, err := sdk.AccAddressFromBech32(sinkAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid sink address: %w", err)
	}

	return &BankSendStrategy{
		chainID:  chainID,
		denom:    denom,
		sinkAddr: sinkAddr,
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

// CreateMsg creates a bank send message from the given address
func (s *BankSendStrategy) CreateMsg(fromAddr string) (sdk.Msg, error) {
	// Validate from address
	_, err := sdk.AccAddressFromBech32(fromAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}

	// Create small amount to send (1 base unit)
	amount := sdk.NewCoins(sdk.NewCoin(s.denom, math.NewInt(1)))

	msg := &banktypes.MsgSend{
		FromAddress: fromAddr,
		ToAddress:   s.sinkAddr,
		Amount:      amount,
	}

	return msg, nil
}

