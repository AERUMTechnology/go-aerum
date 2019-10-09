package params

import (
	"github.com/AERUMTechnology/go-aerum/common"
	"math/big"
)

// Values for AERUMS Genesis related to ATMOS Consensus
var (
	atmosMinDelegateNo           = 3
	atmosNetID                   = 538
	atmosGovernanceAddress       = "0x7f07f6627e9bf1fc821360e0c20f32af532df106"
	atmosTestGovernanceAddress   = "0x02c362540efc9FA5592621C9212D0bF776732050"
	atmosBlockInterval           = uint64(3)
	atmosEpochInterval           = uint64(100)
	atmosGasLimit                = uint64(126000000)
	atmosEthereumRPCProvider     = "https://mainnet.infura.io"
	atmosTestEthereumRPCProvider = "https://rinkeby.infura.io"
	atmosBlockRewards            = new(big.Int).Mul(big.NewInt(888),big.NewInt(1e+18))
)

func NewAtmosMinDelegateNo() int {
	return atmosMinDelegateNo
}

func NewAtmosNetID() int {
	return atmosNetID
}

func NewAtmosGovernanceAddress() common.Address {
	return common.HexToAddress(atmosGovernanceAddress)
}

func NewAtmosTestGovernanceAddress() common.Address {
	return common.HexToAddress(atmosTestGovernanceAddress)
}

func NewAtmosBlockInterval() uint64 {
	return atmosBlockInterval
}

func NewAtmosEpochInterval() uint64 {
	return atmosEpochInterval
}

func NewAtmosGasLimit() uint64 {
	return atmosGasLimit
}

func NewAtmosEthereumRPCProvider() string {
	return atmosEthereumRPCProvider
}

func NewAtmosTestEthereumRPCProvider() string {
	return atmosTestEthereumRPCProvider
}

func NewAtmosBlockRewards() *big.Int {
	return atmosBlockRewards
}

func NewAerumPreAlloc() map[string]string {
	aerumPreAlloc := map[string]string{}
	return aerumPreAlloc
}
