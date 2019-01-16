// Copyright 2016 The go-aerum-new Authors
// This file is part of the go-aerum-new library.
//
// The go-aerum-new library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-aerum-new library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-aerum-new library. If not, see <http://www.gnu.org/licenses/>.

package api

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/AERUMTechnology/go-aerum-new/common"
	"github.com/AERUMTechnology/go-aerum-new/contracts/ens"
	"github.com/AERUMTechnology/go-aerum-new/crypto"
	"github.com/AERUMTechnology/go-aerum-new/node"
	"github.com/AERUMTechnology/go-aerum-new/p2p/discover"
	"github.com/AERUMTechnology/go-aerum-new/swarm/log"
	"github.com/AERUMTechnology/go-aerum-new/swarm/network"
	"github.com/AERUMTechnology/go-aerum-new/swarm/pss"
	"github.com/AERUMTechnology/go-aerum-new/swarm/services/swap"
	"github.com/AERUMTechnology/go-aerum-new/swarm/storage"
)

const (
	DefaultHTTPListenAddr = "127.0.0.1"
	DefaultHTTPPort       = "8500"
)

// separate bzz directories
// allow several bzz nodes running in parallel
type Config struct {
	// serialised/persisted fields
	*storage.FileStoreParams
	*storage.LocalStoreParams
	*network.HiveParams
	Swap *swap.LocalProfile
	Pss  *pss.PssParams
	//*network.SyncParams
	Contract          common.Address
	EnsRoot           common.Address
	EnsAPIs           []string
	Path              string
	ListenAddr        string
	Port              string
	PublicKey         string
	BzzKey            string
	NodeID            string
	NetworkID         uint64
	SwapEnabled       bool
	SyncEnabled       bool
	DeliverySkipCheck bool
	SyncUpdateDelay   time.Duration
	SwapAPI           string
	Cors              string
	BzzAccount        string
	BootNodes         string
	privateKey        *ecdsa.PrivateKey
}

//create a default config with all parameters to set to defaults
func NewConfig() (c *Config) {

	c = &Config{
		LocalStoreParams: storage.NewDefaultLocalStoreParams(),
		FileStoreParams:  storage.NewFileStoreParams(),
		HiveParams:       network.NewHiveParams(),
		//SyncParams:    network.NewDefaultSyncParams(),
		Swap:              swap.NewDefaultSwapParams(),
		Pss:               pss.NewPssParams(),
		ListenAddr:        DefaultHTTPListenAddr,
		Port:              DefaultHTTPPort,
		Path:              node.DefaultDataDir(),
		EnsAPIs:           nil,
		EnsRoot:           ens.TestNetAddress,
		NetworkID:         network.DefaultNetworkID,
		SwapEnabled:       false,
		SyncEnabled:       true,
		DeliverySkipCheck: false,
		SyncUpdateDelay:   15 * time.Second,
		SwapAPI:           "",
		BootNodes:         "",
	}

	return
}

//some config params need to be initialized after the complete
//config building phase is completed (e.g. due to overriding flags)
func (c *Config) Init(prvKey *ecdsa.PrivateKey) {

	address := crypto.PubkeyToAddress(prvKey.PublicKey)
	c.Path = filepath.Join(c.Path, "bzz-"+common.Bytes2Hex(address.Bytes()))
	err := os.MkdirAll(c.Path, os.ModePerm)
	if err != nil {
		log.Error(fmt.Sprintf("Error creating root swarm data directory: %v", err))
		return
	}

	pubkey := crypto.FromECDSAPub(&prvKey.PublicKey)
	pubkeyhex := common.ToHex(pubkey)
	keyhex := crypto.Keccak256Hash(pubkey).Hex()

	c.PublicKey = pubkeyhex
	c.BzzKey = keyhex
	c.NodeID = discover.PubkeyID(&prvKey.PublicKey).String()

	if c.SwapEnabled {
		c.Swap.Init(c.Contract, prvKey)
	}

	c.privateKey = prvKey
	c.LocalStoreParams.Init(c.Path)
	c.LocalStoreParams.BaseKey = common.FromHex(keyhex)

	c.Pss = c.Pss.WithPrivateKey(c.privateKey)
}

func (c *Config) ShiftPrivateKey() (privKey *ecdsa.PrivateKey) {
	if c.privateKey != nil {
		privKey = c.privateKey
		c.privateKey = nil
	}
	return privKey
}
