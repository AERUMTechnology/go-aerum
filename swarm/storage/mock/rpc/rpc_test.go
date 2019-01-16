// Copyright 2018 The go-aerum-new Authors
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

package rpc

import (
	"testing"

	"github.com/AERUMTechnology/go-aerum-new/rpc"
	"github.com/AERUMTechnology/go-aerum-new/swarm/storage/mock/mem"
	"github.com/AERUMTechnology/go-aerum-new/swarm/storage/mock/test"
)

// TestDBStore is running test for a GlobalStore
// using test.MockStore function.
func TestRPCStore(t *testing.T) {
	serverStore := mem.NewGlobalStore()

	server := rpc.NewServer()
	if err := server.RegisterName("mockStore", serverStore); err != nil {
		t.Fatal(err)
	}

	store := NewGlobalStore(rpc.DialInProc(server))
	defer store.Close()

	test.MockStore(t, store, 100)
}
