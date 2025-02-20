package main

import (
	client "benfen-btc/client/v1"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	apisdk "github.com/OpenBlockResource/openblock-api-sdk-go"
	"google.golang.org/grpc"
)

type Utxo struct {
	Address      string `json:"scriptpubkey_address"`
	ScriptPubKey string `json:"scriptpubkey"`
}

type Transaction struct {
	TxID string `json:"txid"`
	Vin  []struct {
		Prevout Utxo `json:"prevout"`
	} `json:"vin"`
	Vout   []Utxo `json:"vout"`
	Status struct {
		Confirmed bool `json:"confirmed"`
	} `json:"status"`
}
type SimpleBtcTransfer struct {
	FromAddress string
	ToAddress   string
	Amount      string
	TxId        string
	Confirmed   bool
	MemoAddress string
}

type BtcFee struct {
	HighFee   int `json:"high_fee_per_kb"`
	LowFee    int `json:"low_fee_per_kb"`
	MediumFee int `json:"medium_fee_per_kb"`
}

var SpiderClient client.TransactionClient
var WalletClient *apisdk.Client
var WalletAddress string

func InitClient(walletAddress, walletApikey, walletSecret, spiderServer string) {
	spider, err := grpc.Dial(spiderServer, grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	SpiderClient = client.NewTransactionClient(spider)
	WalletAddress = walletAddress
	WalletClient = apisdk.NewClient(
		walletApikey,
		walletSecret,
		10*time.Second,
	)
}

// 发送比特币交易审批, 并返回审批ID
func SubmitSendBtcApproval(address, amount string) (string, error) {
	resp, err := SpiderClient.GetUnspentTx(context.Background(), &client.UnspentReq{
		Address:   WalletAddress,
		ChainName: "BTC",
		IsUnspent: "1",
	})
	if err != nil {
		return "", err
	}
	if !resp.Ok {
		return "", fmt.Errorf("Failed to fetch unspent transactions")
	}
	var utxos []*apisdk.Utxo
	for _, utxo := range resp.UtxoList {
		utxos = append(utxos, &apisdk.Utxo{
			Script: utxo.Script,
			Amount: utxo.Amount,
			Hash:   utxo.Hash,
			Index:  utxo.Index,
		})
	}

	feeUrl := "https://api.blockcypher.com/v1/btc/main"
	feeResp, err := http.Get(feeUrl)
	if err != nil {
		return "", err
	}
	defer feeResp.Body.Close()

	var btcFee BtcFee
	if err := json.NewDecoder(feeResp.Body).Decode(&btcFee); err != nil {
		return "", fmt.Errorf("failed to parse fee response: %w", err)
	}
	gasPrice := strconv.Itoa(btcFee.MediumFee / 1000)

	resp2, err := WalletClient.CompanyWallet.NewApproval(&apisdk.ParamNewApproval{
		Action: "TRANSACTION",
		TXInfo: apisdk.TXInfo{
			Chain:           "BTC",
			TransactionType: "transfer",
			To:              address,
			From:            WalletAddress,
			Value:           amount,
			UseMaxAmount:    false,
			GasPrice:        gasPrice,
			Utxo:            utxos,
		},
	})
	if err != nil {
		return "", err
	}
	if !resp2.Ok {
		return "", fmt.Errorf("Failed to submit approval")
	}
	return resp2.Data.OriginRecordId, nil
}

// 扫描比特币交易，并返回交易记录和断点
func ScanBtcTxs(checkpoint string) ([]SimpleBtcTransfer, string, error) {
	lastId, _ := strconv.ParseInt(checkpoint, 10, 64)
	resp, err := SpiderClient.PageList(context.Background(), &client.PageListRequest{
		PageNum:       1,
		PageSize:      50,
		ChainName:     "BTC",
		Address:       WalletAddress,
		OrderBy:       "updated_at asc",
		StartIndex:    lastId,
		DataDirection: 2,
	})
	if err != nil {
		log.Fatalf("Failed to fetch transactions: %v", err)
		return nil, checkpoint, err
	}

	// 处理交易
	var btcTxs []SimpleBtcTransfer
	for _, tx := range resp.List {
		simpleTx := SimpleBtcTransfer{
			FromAddress: tx.FromAddress,
			ToAddress:   tx.ToAddress,
			Amount:      tx.Amount,
			TxId:        tx.TransactionHash,
			Confirmed:   false,
		}
		if tx.Status == "success" {
			simpleTx.Confirmed = true
		}

		if simpleTx.ToAddress == WalletAddress { //对跨入交易，从vout中获取memo地址
			simpleTx.MemoAddress = simpleTx.FromAddress
			url := "https://mempool.space/api/tx/" + simpleTx.TxId
			resp, err := http.Get(url)
			if err != nil {
				return nil, checkpoint, fmt.Errorf("failed to fetch transaction detail: %w", err)
			}
			defer resp.Body.Close()

			var txDetail Transaction
			if err := json.NewDecoder(resp.Body).Decode(&txDetail); err != nil {
				return nil, checkpoint, fmt.Errorf("failed to parse transaction detail: %w", err)
			}
			for _, vout := range txDetail.Vout {
				if len(vout.ScriptPubKey) >= 4 && vout.ScriptPubKey[:4] == "6a20" { //OP_RETURN
					simpleTx.MemoAddress = "0x" + vout.ScriptPubKey[4:]
					break
				}
			}
		}

		btcTxs = append(btcTxs, simpleTx)
		lastId = tx.Cursor
	}
	return btcTxs, strconv.FormatInt(lastId, 10), nil
}

func main() {
	InitClient("bc1qmax35qkxgke2aq2xugaxjl96q8f0k7l3ndhz9g", "361bdf3a1e0640979a3e2240c3361609", "U3IXmFgR848q5XA1vQVgdvW1Z69UvQXD", "IP_ADDRESS:8080")
	aid, err := SubmitSendBtcApproval("bc1qmfg0j2gvegay6zy8638jdlh0fw82w99xkulvkd", "10000000")
	fmt.Println(aid, err)
	//ScanBtcTxs("0")
}
