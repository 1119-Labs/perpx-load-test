package client

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"cosmossdk.io/math"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
)

const localnetDevUSDCIBCDenom = "ibc/8E27BA2D5493AF5636760E354E46004562C46AB7EC0CC4C1CA14E9E20E2545B5"

// generateTxOnce performs a single attempt at generating a transaction.
func (c *PerpxPerpsClient) generateTxOnce() ([]byte, error) {
	// Lazily query the single account tied to this client.
	if err := c.ensureAccountQueried(); err != nil {
		return nil, err
	}

	var seq uint64

	// Choose sequence: either use timestamp nonce (Unix ms) to exercise the
	// accountplus timestamp‑nonce replay protection path, or fall back to the
	// standard incrementing sequence behaviour.
	if c.useTimestampNonce {
		seq = uint64(time.Now().UnixMilli())
	} else {
		// Increment local sequence atomically.
		seq = atomic.AddUint64(&c.sequence, 1) - 1
	}
	return c.generateTxOnceWithSequence(seq)
}

// generateTxOnceWithSequence generates and signs a tx using the provided sequence.
// Callers are responsible for choosing and managing the sequence value.
func (c *PerpxPerpsClient) generateTxOnceWithSequence(seq uint64) ([]byte, error) {
	// Lazily query the single account tied to this client.
	if err := c.ensureAccountQueried(); err != nil {
		return nil, err
	}

	// Use the single client-level account for all perps orders.
	var (
		signerPriv cryptotypes.PrivKey = c.privKey
		signerAddr sdk.AccAddress      = c.addr
		signerAcct uint64              = c.accountNum
	)

	// Delegate message creation to the strategy. This path preserves the
	// existing (non-strict) behavior where the strategy updates local order
	// tracking immediately.
	msg, err := c.strategy.CreateMsg(
		signerAddr.String(),
		c.subaccountNumber,
		c,          // OrderTracker
		c,          // PositionTracker
		&c.nextCID, // Next client ID
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	txBuilder := c.encCfg.TxConfig.NewTxBuilder()
	if err := txBuilder.SetMsgs(msg); err != nil {
		return nil, fmt.Errorf("failed to set message: %w", err)
	}

	// Reuse the same gas/fee config as the bank client, but be denom‑aware for
	// the tracing dev stack. For the default native token (`aperpx`), we match
	// the chain's minimum gas price of 25000000000aperpx per unit of gas
	// For the dev USDC IBC denom used for perps margin, we match the chain's configured minimum of 0.025 per
	// unit of gas, which corresponds to 25000 quantums at 6 decimals.
	gasLimit := uint64(200000)
	if c.config.Perps.FeeGasLimit > 0 {
		gasLimit = c.config.Perps.FeeGasLimit
	}

	minGasPrice := math.NewInt(25000000000) // default for aperpx
	if c.config.Perps.FeeMinGasPrice != "" {
		if v, ok := math.NewIntFromString(c.config.Perps.FeeMinGasPrice); ok {
			minGasPrice = v
		} else {
			return nil, fmt.Errorf("invalid loadtest.perps.feeMinGasPrice: %q", c.config.Perps.FeeMinGasPrice)
		}
	}

	altDenom := localnetDevUSDCIBCDenom
	if c.config.Perps.FeeAltDenom != "" {
		altDenom = c.config.Perps.FeeAltDenom
	}
	if c.strategy.Denom() == altDenom {
		altMinGasPrice := math.NewInt(25000)
		if c.config.Perps.FeeAltMinGasPrice != "" {
			if v, ok := math.NewIntFromString(c.config.Perps.FeeAltMinGasPrice); ok {
				altMinGasPrice = v
			} else {
				return nil, fmt.Errorf("invalid loadtest.perps.feeAltMinGasPrice: %q", c.config.Perps.FeeAltMinGasPrice)
			}
		}
		minGasPrice = altMinGasPrice
	}
	feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
	feeCoins := sdk.NewCoins(sdk.NewCoin(c.strategy.Denom(), feeAmount))
	txBuilder.SetFeeAmount(feeCoins)
	txBuilder.SetGasLimit(gasLimit)

	// First round: set empty signature.
	sigV2Empty := signing.SignatureV2{
		PubKey: signerPriv.PubKey(),
		Data: &signing.SingleSignatureData{
			SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
			Signature: nil,
		},
		Sequence: seq,
	}
	if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
		return nil, fmt.Errorf("failed to set empty signature: %w", err)
	}

	signerData := authsigning.SignerData{
		Address:       signerAddr.String(),
		ChainID:       c.strategy.ChainID(),
		AccountNumber: signerAcct,
		Sequence:      seq,
		PubKey:        signerPriv.PubKey(),
	}

	sigV2, err := tx.SignWithPrivKey(
		context.Background(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder,
		signerPriv,
		c.encCfg.TxConfig,
		seq,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %w", err)
	}

	if err := txBuilder.SetSignatures(sigV2); err != nil {
		return nil, fmt.Errorf("failed to set signature: %w", err)
	}

	encoder := c.chainAPI.TxEncoder()
	txBytes, err := encoder(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	return txBytes, nil
}

// generateTxOnceWithSequenceAndEffects generates and signs a tx using the provided
// sequence and returns an "apply" callback that should be invoked only after the
// tx is accepted (CheckTx code == 0) to keep local order tracking consistent.
func (c *PerpxPerpsClient) generateTxOnceWithSequenceAndEffects(seq uint64) ([]byte, func(), error) {
	txBytes, applySuccess, _, err := c.generateTxOnceWithSequenceAndEffectsAndCleanup(seq)
	return txBytes, applySuccess, err
}

// generateTxOnceWithSequenceAndEffectsAndCleanup is like generateTxOnceWithSequenceAndEffects,
// but also returns a "cleanup" callback that is safe to run on specific CheckTx
// rejections to keep local order tracking from getting stuck (e.g. canceling an
// order that no longer exists on-chain).
func (c *PerpxPerpsClient) generateTxOnceWithSequenceAndEffectsAndCleanup(seq uint64) ([]byte, func(), func(), error) {
	// Lazily query the single account tied to this client.
	if err := c.ensureAccountQueried(); err != nil {
		return nil, nil, nil, err
	}

	// Use the single client-level account for all perps orders.
	var (
		signerPriv cryptotypes.PrivKey = c.privKey
		signerAddr sdk.AccAddress      = c.addr
		signerAcct uint64              = c.accountNum
	)

	msg, eff, err := c.strategy.CreateMsgWithEffects(
		signerAddr.String(),
		c.subaccountNumber,
		c,          // OrderTracker (read-only usage for choosing cancels)
		c,          // PositionTracker
		&c.nextCID, // Next client ID
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create message: %w", err)
	}

	apply := func() {}
	cleanup := func() {}
	if eff != nil {
		// Capture a copy for the closure.
		e := *eff
		apply = func() {
			if e.Track != nil {
				c.TrackOrder(*e.Track)
			}
			if e.Remove != nil {
				c.RemoveOrder(*e.Remove)
			}
			if len(e.RemoveMany) > 0 {
				c.RemoveOrders(e.RemoveMany)
			}
		}
		// Cleanup should never add new tracked orders; only remove.
		cleanup = func() {
			if e.Remove != nil {
				c.RemoveOrder(*e.Remove)
			}
			if len(e.RemoveMany) > 0 {
				c.RemoveOrders(e.RemoveMany)
			}
		}
	}

	txBuilder := c.encCfg.TxConfig.NewTxBuilder()
	if err := txBuilder.SetMsgs(msg); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to set message: %w", err)
	}

	gasLimit := uint64(200000)
	if c.config.Perps.FeeGasLimit > 0 {
		gasLimit = c.config.Perps.FeeGasLimit
	}

	minGasPrice := math.NewInt(25000000000) // default for aperpx
	if c.config.Perps.FeeMinGasPrice != "" {
		if v, ok := math.NewIntFromString(c.config.Perps.FeeMinGasPrice); ok {
			minGasPrice = v
		} else {
			return nil, nil, nil, fmt.Errorf("invalid loadtest.perps.feeMinGasPrice: %q", c.config.Perps.FeeMinGasPrice)
		}
	}

	altDenom := localnetDevUSDCIBCDenom
	if c.config.Perps.FeeAltDenom != "" {
		altDenom = c.config.Perps.FeeAltDenom
	}
	if c.strategy.Denom() == altDenom {
		altMinGasPrice := math.NewInt(25000)
		if c.config.Perps.FeeAltMinGasPrice != "" {
			if v, ok := math.NewIntFromString(c.config.Perps.FeeAltMinGasPrice); ok {
				altMinGasPrice = v
			} else {
				return nil, nil, nil, fmt.Errorf("invalid loadtest.perps.feeAltMinGasPrice: %q", c.config.Perps.FeeAltMinGasPrice)
			}
		}
		minGasPrice = altMinGasPrice
	}
	feeAmount := minGasPrice.Mul(math.NewInt(int64(gasLimit)))
	feeCoins := sdk.NewCoins(sdk.NewCoin(c.strategy.Denom(), feeAmount))
	txBuilder.SetFeeAmount(feeCoins)
	txBuilder.SetGasLimit(gasLimit)

	// First round: set empty signature.
	sigV2Empty := signing.SignatureV2{
		PubKey: signerPriv.PubKey(),
		Data: &signing.SingleSignatureData{
			SignMode:  signing.SignMode_SIGN_MODE_DIRECT,
			Signature: nil,
		},
		Sequence: seq,
	}
	if err := txBuilder.SetSignatures(sigV2Empty); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to set empty signature: %w", err)
	}

	signerData := authsigning.SignerData{
		Address:       signerAddr.String(),
		ChainID:       c.strategy.ChainID(),
		AccountNumber: signerAcct,
		Sequence:      seq,
		PubKey:        signerPriv.PubKey(),
	}

	sigV2, err := tx.SignWithPrivKey(
		context.Background(),
		signing.SignMode_SIGN_MODE_DIRECT,
		signerData,
		txBuilder,
		signerPriv,
		c.encCfg.TxConfig,
		seq,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to sign: %w", err)
	}

	if err := txBuilder.SetSignatures(sigV2); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to set signature: %w", err)
	}

	encoder := c.chainAPI.TxEncoder()
	txBytes, err := encoder(txBuilder.GetTx())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	return txBytes, apply, cleanup, nil
}
