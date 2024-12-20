package feeoraclev2

import (
	"context"
	"math/big"

	"github.com/omni-network/omni/contracts/bindings"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/ethclient/ethbackend"
	"github.com/omni-network/omni/lib/evmchain"
	"github.com/omni-network/omni/lib/log"
	"github.com/omni-network/omni/lib/tokens"
	"github.com/omni-network/omni/monitor/xfeemngr/gasprice"

	"github.com/ethereum/go-ethereum/params"
)

func feeParams(ctx context.Context, srcChainID uint64, destChainIDs []uint64, backends ethbackend.Backends, pricer tokens.Pricer,
) ([]bindings.IFeeOracleV2FeeParams, error) {
	// used cached pricer, to avoid multiple price requests for same token
	pricer = tokens.NewCachedPricer(pricer)

	srcChain, ok := evmchain.MetadataByID(srcChainID)
	if !ok {
		return nil, errors.New("meta by chain id", "chain_id", srcChainID)
	}

	var resp []bindings.IFeeOracleV2FeeParams
	for _, destChainID := range destChainIDs {
		destChain, ok := evmchain.MetadataByID(destChainID)
		if !ok {
			return nil, errors.New("meta by chain id", "dest_chain", destChain.Name)
		}

		ps := destFeeParams(ctx, srcChain, destChain, backends, pricer)

		resp = append(resp, ps)
	}

	return resp, nil
}

// feeParams returns the fee parameters for the given source token and destination chains.
func destFeeParams(ctx context.Context, srcChain evmchain.Metadata, destChain evmchain.Metadata, backends ethbackend.Backends, pricer tokens.Pricer,
) bindings.IFeeOracleV2FeeParams {
	// conversion rate from "dest token" to "src token"
	// ex if dest chain is ETH, and src chain is OMNI, we need to know the rate of ETH to OMNI.
	toNativeRate, err := conversionRate(ctx, pricer, destChain.NativeToken, srcChain.NativeToken)
	if err != nil {
		if srcChain.NativeToken == destChain.NativeToken {
			toNativeRate = 1 // 1 ETH = 1 ETH || 1 OMNI = 1 OMNI
		} else if destChain.NativeToken == tokens.OMNI {
			toNativeRate = 0.0025 // 1 OMNI = 0.0025 ETH
		} else {
			toNativeRate = 400 // 1 ETH = 400 OMNI
		}
		log.Warn(ctx, "Failed fetching conversion rate, using default", err, "dest_chain", destChain.Name, "src_chain", srcChain.Name, "to_native_rate", toNativeRate)
	}

	// Get execution gas price, defaulting to 1 Gwei if any error occurs.
	var execBackend *ethbackend.Backend
	var execGasPrice *big.Int
	execBackend, err = backends.Backend(destChain.ChainID)
	if err != nil {
		log.Warn(ctx, "Failed getting exec backend, using default 1 Gwei", err, "dest_chain", destChain.Name)
		execGasPrice = big.NewInt(params.GWei)
	} else {
		execGasPrice, err = execBackend.SuggestGasPrice(ctx)
		if err != nil {
			log.Warn(ctx, "Failed fetching exec gas price, using default 1 Gwei", err, "dest_chain", destChain.Name)
			execGasPrice = big.NewInt(params.GWei)
		}
	}

	// Get data gas price, defaulting to 1 Gwei if any error occurs.
	var dataBackend *ethbackend.Backend
	var dataGasPrice *big.Int
	dataBackend, err = backends.Backend(destChain.PostsTo)
	if err != nil {
		log.Warn(ctx, "Failed getting data backend, using default 1 Gwei", err, "dest_chain", destChain.Name)
		dataGasPrice = big.NewInt(params.GWei)
	} else {
		dataGasPrice, err = dataBackend.SuggestGasPrice(ctx)
		if err != nil {
			log.Warn(ctx, "Failed fetching data gas price, using default 1 Gwei", err, "dest_chain", destChain.Name)
			dataGasPrice = big.NewInt(params.GWei)
		}
	}

	return bindings.IFeeOracleV2FeeParams{
		ChainId:      destChain.ChainID,
		ExecGasPrice: gasprice.Tier(execGasPrice.Uint64()),
		DataGasPrice: gasprice.Tier(dataGasPrice.Uint64()),
		ToNativeRate: rateToNumerator(toNativeRate),
	}
}

// conversionRate returns the conversion rate C from token F to token T, where C = price(F) / price(T).
// Ex. We want to convert from ETH to OMNI. We need to know the what X OMNI = 1 ETH.
// If the price of OMNI is 10, the price of ETH is 1000. The conversion rate C is price(ETH) / price(OMNI) = 1000 / 10 = 100.
func conversionRate(ctx context.Context, pricer tokens.Pricer, from, to tokens.Token) (float64, error) {
	if from == to {
		return 1, nil
	}

	prices, err := pricer.Price(ctx, from, to)
	if err != nil {
		return 0, errors.Wrap(err, "get price", "ids", "from", from, "to", to)
	}

	has := func(t tokens.Token) bool {
		p, ok := prices[t]
		return ok && p > 0
	}
	if !has(to) {
		return 0, errors.New("missing to token price", "to", to)
	} else if !has(from) {
		return 0, errors.New("missing from token price", "from", from)
	}

	return prices[from] / prices[to], nil
}

// conversionRateDenom matches the CONVERSION_RATE_DENOM on the FeeOracleV2 contract.
// This denominator helps convert between token amounts in solidity, in which there are no floating point numbers.
//
//	ex. (amt A) * (rate R) / CONVERSION_RATE_DENOM = (amt B)
var conversionRateDenom = big.NewInt(1_000_000)

// rateToNumerator translates a float rate (ex 0.1) to numerator / CONVERSION_RATE_DENOM (ex 100_000).
// This rate-as-numerator representation is used in FeeOracleV2 contracts.
func rateToNumerator(r float64) uint64 {
	denom := new(big.Float).SetInt64(conversionRateDenom.Int64())
	numer := new(big.Float).SetFloat64(r)
	norm, _ := new(big.Float).Mul(numer, denom).Uint64()

	return norm
}
