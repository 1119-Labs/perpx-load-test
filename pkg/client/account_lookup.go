package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// AccountInfo holds the minimal on-chain account metadata needed for signing.
type AccountInfo struct {
	AccountNumber uint64
	Sequence      uint64
}

// QueryAccountInfo queries the /cosmos/auth/v1beta1/accounts REST endpoint for
// the given address and extracts the account number and sequence. It is shared
// between the bank and perps clients so that account lookup behavior and error
// handling stay consistent.
func QueryAccountInfo(restURL, bech32Addr string, httpClient *http.Client) (AccountInfo, error) {
	if httpClient == nil {
		httpClient = sharedHTTPClient
	}

	addr, err := sdk.AccAddressFromBech32(bech32Addr)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("invalid bech32 address %q: %w", bech32Addr, err)
	}

	url := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", restURL, addr.String())
	resp, err := httpClient.Get(url)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("failed to query account %s: %w", addr.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return AccountInfo{}, fmt.Errorf("unexpected status code %d when querying account %s: %s", resp.StatusCode, addr.String(), string(body))
	}

	var payload struct {
		Account struct {
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return AccountInfo{}, fmt.Errorf("failed to decode account response for %s: %w", addr.String(), err)
	}

	accountNumber, err := strconv.ParseUint(payload.Account.AccountNumber, 10, 64)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("invalid account_number %q for %s: %w", payload.Account.AccountNumber, addr.String(), err)
	}
	sequence, err := strconv.ParseUint(payload.Account.Sequence, 10, 64)
	if err != nil {
		return AccountInfo{}, fmt.Errorf("invalid sequence %q for %s: %w", payload.Account.Sequence, addr.String(), err)
	}

	return AccountInfo{
		AccountNumber: accountNumber,
		Sequence:      sequence,
	}, nil
}

