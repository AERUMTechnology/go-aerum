#!/bin/bash
$PWD/../../build/bin/aerum  --datadir $PWD/nodes/node2/data --atmos.testnet --mine --gasprice "1"  --syncmode "full" --networkid 2018 --port 2222 console --unlock  $(cat $PWD/nodes/node2/account.txt|cut -f 2 -d " "|sed 's/{//g'|sed 's/}//g') --password <(echo "node2")
