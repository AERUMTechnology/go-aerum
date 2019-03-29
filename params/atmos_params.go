package params

import (
	"github.com/AERUMTechnology/go-aerum/common"
	"math/big"
)

// Values for AERUMS Genesis related to ATMOS Consensus
var (
	atmosMinDelegateNo       = 3
	atmosNetID               = 538
	atmosGovernanceAddress   = "0x7f07f6627e9bf1fc821360e0c20f32af532df106"
	atmosBlockInterval       = uint64(2)
	atmosEpochInterval       = uint64(1000)
	atmosGasLimit            = uint64(21000000)
	atmosEthereumRPCProvider = "https://mainnet.infura.io"
	atmosBlockRewards        = big.NewInt(0.487e+18)
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

func NewAtmosBlockRewards() *big.Int {
	return atmosBlockRewards
}

func NewAerumPreAlloc() map[string]string {
	aerumPreAlloc := map[string]string{}
	return aerumPreAlloc
}
