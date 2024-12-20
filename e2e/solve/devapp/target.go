package devapp

import (
	"math/big"

	"github.com/omni-network/omni/contracts/bindings"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/evmchain"
	solver "github.com/omni-network/omni/solver/types"

	"github.com/ethereum/go-ethereum/common"
)

var _ solver.Target = (*App)(nil)

type DepositArgs struct {
	OnBehalfOf common.Address
	Amount     *big.Int
}

func (App) ChainID() uint64 {
	return evmchain.IDMockL1
}

func (t App) Address() common.Address {
	return t.L1Vault
}

func (t App) IsAllowedCall(call bindings.SolveCall) bool {
	if call.DestChainId != t.ChainID() {
		return false
	}

	if call.Target != t.Address() {
		return false
	}

	_, err := unpackDeposit(call.Data)

	return err == nil
}

func (t App) TokenPrereqs(call bindings.SolveCall) ([]bindings.SolveTokenPrereq, error) {
	if !t.IsAllowedCall(call) {
		return nil, errors.New("call not allowed")
	}

	args, err := unpackDeposit(call.Data)
	if err != nil {
		return nil, errors.Wrap(err, "unpack deposit")
	}

	return []bindings.SolveTokenPrereq{
		{
			Token:  t.L1Token,
			Amount: args.Amount,
		},
	}, nil
}

func (t App) Verify(srcChainID uint64, call bindings.SolveCall, deposits []bindings.SolveDeposit) error {
	// we only accept deposits from mock L2
	if srcChainID != evmchain.IDMockL2 {
		return errors.New("source chain not supported", "src", srcChainID)
	}

	if !t.IsAllowedCall(call) {
		return errors.New("call not allowed")
	}

	args, err := unpackDeposit(call.Data)
	if err != nil {
		return errors.Wrap(err, "invalid deposit")
	}

	var l2token *bindings.SolveDeposit

	for _, deposit := range deposits {
		if deposit.Token == t.L2Token {
			l2token = &deposit
		}
	}

	// if no l2 deposit, we can't accept
	if l2token == nil {
		return errors.New("no L2 token deposit")
	}

	// if l2 deposit amount does not match call amount, we can't accept
	if l2token.Amount.Cmp(args.Amount) != 0 {
		return errors.New("insufficient L2 token deposit")
	}

	// TODO: require native deposit that covers gas / risk / overhead

	return nil
}

func unpackDeposit(data []byte) (DepositArgs, error) {
	unpacked, err := vaultDeposit.Inputs.Unpack(data)
	if err != nil {
		return DepositArgs{}, errors.Wrap(err, "unpack data")
	}

	var args DepositArgs
	if err := vaultDeposit.Inputs.Copy(&args, unpacked); err != nil {
		return DepositArgs{}, errors.Wrap(err, "copy args")
	}

	return args, nil
}
