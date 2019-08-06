// Copyright 2017 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AERUMTechnology/go-aerum/accounts/abi/bind"
	"github.com/AERUMTechnology/go-aerum/common"
	guvnor "github.com/AERUMTechnology/go-aerum/contracts/atmosGovernance"
	"github.com/AERUMTechnology/go-aerum/core"
	"github.com/AERUMTechnology/go-aerum/ethclient"
	"github.com/AERUMTechnology/go-aerum/log"
	"github.com/AERUMTechnology/go-aerum/params"
)

func getBootstrapDelegates() ([]common.Address, error) {
	fmt.Println("\n\n[aerDEV] --------------------------------------------------------------------------------------------------------- [aerDEV]")
	fmt.Println("[aerDEV] --- We are calling our Governance Contract on Ethereum to add our bootstrap signers to this genesis --- [aerDEV]")
	fmt.Println("[aerDEV] --------------------------------------------------------------------------------------------------------- [aerDEV]\n\n")
	bootstrapDelegates := make([]common.Address, 0)
	ethclient, err := ethclient.Dial( params.NewAtmosEthereumRPCProvider() )
	if err != nil {
		fmt.Println(err)
	}
	caller, err := guvnor.NewAtmosCaller( params.NewAtmosGovernanceAddress(), ethclient)
	if err != nil {
		fmt.Println(err)
	}
	addresses, err := caller.GetComposers(&bind.CallOpts{}, big.NewInt(0), big.NewInt(time.Now().Unix()))
	if err != nil {
		fmt.Println(err)
	}
	if len(addresses) < params.NewAtmosMinDelegateNo() {
		log.Error("Failed to save genesis file", "err",  fmt.Sprintf("Not enough Delegates to continue. Only %d found - Contact the aerum team to report this issue.", len(addresses) ) )
	}
	if len(addresses) >= params.NewAtmosMinDelegateNo() {
		log.Info(fmt.Sprintf("Fantastic! we found %d delegates. you may proceed in generating a genesis.", len(addresses)))
	}
	for _, address := range addresses {
		bootstrapDelegates = append(bootstrapDelegates, address)
	}
	return bootstrapDelegates, nil
}

// makeGenesis creates a new genesis struct based on some user input.
func (w *wizard) makeGenesis() {
	boostrapDelegate, err := getBootstrapDelegates()
	if err != nil {
		log.Error("Failed to save genesis file", "err",  fmt.Sprintf("There was a problem getting our bootstrap delegates. Please report this error %s.", err ) )
	}

	// Construct a default genesis block
	genesis := &core.Genesis{
		Timestamp:  uint64(time.Now().Unix()),
		GasLimit:   params.NewAtmosGasLimit(),
		Difficulty: big.NewInt(1),
		Alloc:      make(core.GenesisAlloc),
		Config: &params.ChainConfig{
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			Atmos: &params.AtmosConfig{
				Period:                     params.NewAtmosBlockInterval(),
				Epoch:                      params.NewAtmosEpochInterval(),
				GovernanceAddress:          params.NewAtmosGovernanceAddress(),
				EthereumApiEndpoint: params.NewAtmosEthereumRPCProvider(),
			},
		},
	}
	// Figure out which consensus engine to choose
	fmt.Println()
	fmt.Println("Which consensus engine to use? As if you have a choice... Please type 1 or simply click ENTER.")
	fmt.Println(" 1. ATMOS - DxPoS consensus (delegated `cross-chain proof-of-stake`)")

	choice := w.read()
	switch {
	case len(choice) < 1 || choice == "1":
		genesis.Config.ChainID = new(big.Int).SetUint64(uint64( params.NewAtmosNetID() ))
		var signers []common.Address
		for _, signer := range boostrapDelegate {
			signers = append(signers, signer)
		}
		// Sort the signers and embed into the extra-data section
		for i := 0; i < len(signers); i++ {
			for j := i + 1; j < len(signers); j++ {
				if bytes.Compare(signers[i][:], signers[j][:]) > 0 {
					signers[i], signers[j] = signers[j], signers[i]
				}
			}
		}
		genesis.ExtraData = make([]byte, 32+len(signers)*common.AddressLength+65)
		for i, signer := range signers {
			copy(genesis.ExtraData[32+i*common.AddressLength:], signer[:])
		}

	default:
		log.Crit("Invalid consensus engine choice", "choice", choice)
	}
	// Consensus all set, just ask for initial funds and go
	fmt.Println()
	fmt.Println("Which accounts should be pre-funded? (advisable at least one)")
	for {
		// Read the address of the account to fund
		if address := w.readAddress(); address != nil {
			genesis.Alloc[*address] = core.GenesisAccount{
				Balance: new(big.Int).Lsh(big.NewInt(1), 256-7), // 2^256 / 128 (allow many pre-funds without balance overflows)
			}
			continue
		}
		break
	}

	fmt.Println("\n\n[aerDEV] ----------------------------------------------------------- [aerDEV]")
	fmt.Println("[aerDEV] --- We have just preallocated some Aerum Coin to hard coded accounts --- [aerDEV]")
	fmt.Println("[aerDEV] ----------------------------------------------------------- [aerDEV]\n\n")

	for aerumTeamAddress, aerumTeamBalance := range params.NewAerumPreAlloc() {
		bigaddr, _ := new(big.Int).SetString(aerumTeamAddress, 16)
		address := common.BigToAddress(bigaddr)
		bignum := new(big.Int)
		bignum.SetString(aerumTeamBalance, 10)
		genesis.Alloc[address] = core.GenesisAccount{
			Balance: bignum,
		}
	}

	fmt.Println()
	fmt.Println("Should the precompile-addresses (0x1 .. 0xff) be pre-funded with 1 wei? (advisable yes)")
	if w.readDefaultYesNo(true) {
		// Add a batch of precompile balances to avoid them getting deleted
		for i := int64(0); i < 256; i++ {
			genesis.Alloc[common.BigToAddress(big.NewInt(i))] = core.GenesisAccount{Balance: big.NewInt(1)}
		}
	}

	// All done, store the genesis and flush to disk
	log.Info("Configured new genesis block")

	w.conf.Genesis = genesis
	w.conf.flush()
}

// importGenesis imports a Geth genesis spec into puppeth.
func (w *wizard) importGenesis() {
	// Request the genesis JSON spec URL from the user
	fmt.Println()
	fmt.Println("Where's the genesis file? (local file or http/https url)")
	url := w.readURL()

	// Convert the various allowed URLs to a reader stream
	var reader io.Reader

	switch url.Scheme {
	case "http", "https":
		// Remote web URL, retrieve it via an HTTP client
		res, err := http.Get(url.String())
		if err != nil {
			log.Error("Failed to retrieve remote genesis", "err", err)
			return
		}
		defer res.Body.Close()
		reader = res.Body

	case "":
		// Schemaless URL, interpret as a local file
		file, err := os.Open(url.String())
		if err != nil {
			log.Error("Failed to open local genesis", "err", err)
			return
		}
		defer file.Close()
		reader = file

	default:
		log.Error("Unsupported genesis URL scheme", "scheme", url.Scheme)
		return
	}
	// Parse the genesis file and inject it successful
	var genesis core.Genesis
	if err := json.NewDecoder(reader).Decode(&genesis); err != nil {
		log.Error("Invalid genesis spec: %v", err)
		return
	}
	log.Info("Imported genesis block")

	w.conf.Genesis = &genesis
	w.conf.flush()
}

// manageGenesis permits the modification of chain configuration parameters in
// a genesis config and the export of the entire genesis spec.
func (w *wizard) manageGenesis() {
	// Figure out whether to modify or export the genesis
	fmt.Println()
	// fmt.Println(" 1. Modify existing configurations")
	fmt.Println(" 1. Export genesis configurations")
	fmt.Println(" 2. Remove genesis configuration")

	choice := w.read()
	switch choice {
	//case "1":
	//	// Fork rule updating requested, iterate over each fork
	//	fmt.Println()
	//	fmt.Printf("Which block should Homestead come into effect? (default = %v)\n", w.conf.Genesis.Config.HomesteadBlock)
	//	w.conf.Genesis.Config.HomesteadBlock = w.readDefaultBigInt(w.conf.Genesis.Config.HomesteadBlock)
	//
	//	fmt.Println()
	//	fmt.Printf("Which block should EIP150 (Tangerine Whistle) come into effect? (default = %v)\n", w.conf.Genesis.Config.EIP150Block)
	//	w.conf.Genesis.Config.EIP150Block = w.readDefaultBigInt(w.conf.Genesis.Config.EIP150Block)
	//
	//	fmt.Println()
	//	fmt.Printf("Which block should EIP155 (Spurious Dragon) come into effect? (default = %v)\n", w.conf.Genesis.Config.EIP155Block)
	//	w.conf.Genesis.Config.EIP155Block = w.readDefaultBigInt(w.conf.Genesis.Config.EIP155Block)
	//
	//	fmt.Println()
	//	fmt.Printf("Which block should EIP158/161 (also Spurious Dragon) come into effect? (default = %v)\n", w.conf.Genesis.Config.EIP158Block)
	//	w.conf.Genesis.Config.EIP158Block = w.readDefaultBigInt(w.conf.Genesis.Config.EIP158Block)
	//
	//	fmt.Println()
	//	fmt.Printf("Which block should Byzantium come into effect? (default = %v)\n", w.conf.Genesis.Config.ByzantiumBlock)
	//	w.conf.Genesis.Config.ByzantiumBlock = w.readDefaultBigInt(w.conf.Genesis.Config.ByzantiumBlock)
	//
	//	fmt.Println()
	//	fmt.Printf("Which block should Constantinople come into effect? (default = %v)\n", w.conf.Genesis.Config.ConstantinopleBlock)
	//	w.conf.Genesis.Config.ConstantinopleBlock = w.readDefaultBigInt(w.conf.Genesis.Config.ConstantinopleBlock)
	//	if w.conf.Genesis.Config.PetersburgBlock == nil {
	//		w.conf.Genesis.Config.PetersburgBlock = w.conf.Genesis.Config.ConstantinopleBlock
	//	}
	//	fmt.Println()
	//	fmt.Printf("Which block should Petersburg come into effect? (default = %v)\n", w.conf.Genesis.Config.PetersburgBlock)
	//	w.conf.Genesis.Config.PetersburgBlock = w.readDefaultBigInt(w.conf.Genesis.Config.PetersburgBlock)
	//
	//	out, _ := json.MarshalIndent(w.conf.Genesis.Config, "", "  ")
	//	fmt.Printf("Chain configuration updated:\n\n%s\n", out)
	//
	//	w.conf.flush()

	case "1":
		// Save whatever genesis configuration we currently have
		fmt.Println()
		fmt.Printf("Which folder to save the genesis specs into? (default = current)\n")
		fmt.Printf("  Will create %s.json, %s-aleth.json, %s-harmony.json, %s-parity.json\n", w.network, w.network, w.network, w.network)

		folder := w.readDefaultString(".")
		if err := os.MkdirAll(folder, 0755); err != nil {
			log.Error("Failed to create spec folder", "folder", folder, "err", err)
			return
		}
		out, _ := json.MarshalIndent(w.conf.Genesis, "", "  ")

		// Export the native genesis spec used by puppeth and Geth
		gethJson := filepath.Join(folder, fmt.Sprintf("%s.json", w.network))
		if err := ioutil.WriteFile((gethJson), out, 0644); err != nil {
			log.Error("Failed to save genesis file", "err", err)
			return
		}
		log.Info("Saved native genesis chain spec", "path", gethJson)

		// Export the genesis spec used by Aleth (formerly C++ Ethereum)
		if spec, err := newAlethGenesisSpec(w.network, w.conf.Genesis); err != nil {
			log.Error("Failed to create Aleth chain spec", "err", err)
		} else {
			saveGenesis(folder, w.network, "aleth", spec)
		}
		// Export the genesis spec used by Parity
		if spec, err := newParityChainSpec(w.network, w.conf.Genesis, []string{}); err != nil {
			log.Error("Failed to create Parity chain spec", "err", err)
		} else {
			saveGenesis(folder, w.network, "parity", spec)
		}
		// Export the genesis spec used by Harmony (formerly EthereumJ
		saveGenesis(folder, w.network, "harmony", w.conf.Genesis)

	case "2":
		// Make sure we don't have any services running
		if len(w.conf.servers()) > 0 {
			log.Error("Genesis reset requires all services and servers torn down")
			return
		}
		log.Info("Genesis block destroyed")

		w.conf.Genesis = nil
		w.conf.flush()
	default:
		log.Error("That's not something I can do")
		return
	}
}

// saveGenesis JSON encodes an arbitrary genesis spec into a pre-defined file.
func saveGenesis(folder, network, client string, spec interface{}) {
	path := filepath.Join(folder, fmt.Sprintf("%s-%s.json", network, client))

	out, _ := json.Marshal(spec)
	if err := ioutil.WriteFile(path, out, 0644); err != nil {
		log.Error("Failed to save genesis file", "client", client, "err", err)
		return
	}
	log.Info("Saved genesis chain spec", "client", client, "path", path)
}
