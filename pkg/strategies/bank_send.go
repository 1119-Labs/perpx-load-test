package strategies

import (
	"fmt"
	"math/rand"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// BankSendStrategy handles creation of bank send messages
type BankSendStrategy struct {
	chainID      string
	denom        string
	sinkAddr     string
	receiverPool []string // if set, pick random receiver from pool (excluding sender) to avoid sequential tx bottleneck
}

// NewBankSendStrategy creates a new bank send strategy.
// If receiverPool is non-nil and non-empty, each CreateMsg picks a random receiver from the pool
// (excluding fromAddr) so different senders send to different receivers and txs need not execute sequentially.
// Otherwise sinkAddr is used as the single receiver.
func NewBankSendStrategy(chainID, denom, sinkAddr string, receiverPool []string) (*BankSendStrategy, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain ID cannot be empty")
	}
	if denom == "" {
		return nil, fmt.Errorf("denom cannot be empty")
	}
	if len(receiverPool) == 0 && sinkAddr == "" {
		return nil, fmt.Errorf("either sink address or receiver pool must be set")
	}
	if sinkAddr != "" {
		if _, err := sdk.AccAddressFromBech32(sinkAddr); err != nil {
			return nil, fmt.Errorf("invalid sink address: %w", err)
		}
	}
	for i, addr := range receiverPool {
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			return nil, fmt.Errorf("invalid receiver pool address at index %d: %w", i, err)
		}
	}

	return &BankSendStrategy{
		chainID:      chainID,
		denom:        denom,
		sinkAddr:     sinkAddr,
		receiverPool: receiverPool,
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

// CreateMsg creates a bank send message from the given address.
// When receiverPool is set, picks a random receiver different from fromAddr so txs can execute in parallel.
func (s *BankSendStrategy) CreateMsg(fromAddr string) (sdk.Msg, error) {
	_, err := sdk.AccAddressFromBech32(fromAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid from address: %w", err)
	}

	toAddr := s.sinkAddr
	if len(s.receiverPool) > 0 {
		// Pick a random receiver different from sender to avoid sequential execution dependency
		candidates := s.receiverPool
		if len(candidates) == 1 && candidates[0] == fromAddr {
			toAddr = candidates[0] // fallback: self (avoid empty slice)
		} else {
			for {
				toAddr = candidates[rand.Intn(len(candidates))]
				if toAddr != fromAddr {
					break
				}
				if len(candidates) == 1 {
					break
				}
			}
		}
	}

	amount := sdk.NewCoins(sdk.NewCoin(s.denom, math.NewInt(1)))
	msg := &banktypes.MsgSend{
		FromAddress: fromAddr,
		ToAddress:  toAddr,
		Amount:     amount,
	}
	return msg, nil
}

