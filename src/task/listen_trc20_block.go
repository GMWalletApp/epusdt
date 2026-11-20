package task

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	tron "github.com/GMWalletApp/epusdt/crypto"
	"github.com/GMWalletApp/epusdt/model/data"
	"github.com/GMWalletApp/epusdt/model/mdb"
	"github.com/GMWalletApp/epusdt/model/service"
	"github.com/GMWalletApp/epusdt/util/log"
)

const (
	// transfer(address,uint256) 方法签名前4字节
	TransferMethodID = "a9059cbb"

	PollInterval   = 3 * time.Second
	RequestTimeout = 10 * time.Second
)

func HexToTronAddress(hexAddr string) (string, error) {
	hexAddr = strings.TrimPrefix(hexAddr, "0x")
	hexAddr = strings.TrimPrefix(hexAddr, "0X")

	raw, err := hex.DecodeString(hexAddr)
	if err != nil {
		return "", err
	}

	// 确保有 0x41 前缀（TRON 主网地址前缀）
	if len(raw) == 20 {
		raw = append([]byte{0x41}, raw...)
	}
	if len(raw) != 21 {
		return "", fmt.Errorf("地址长度非法: %d bytes", len(raw))
	}

	return tron.EncodeCheck(raw), nil
}

func TronAddressToHex(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("地址为空")
	}
	raw, err := tron.DecodeCheck(addr)
	if err != nil {
		return "", err
	}
	if len(raw) != 21 {
		return "", fmt.Errorf("地址长度非法: %d bytes", len(raw))
	}
	if raw[0] != 0x41 {
		return "", fmt.Errorf("非法 TRON 主网地址前缀: 0x%x", raw[0])
	}
	return strings.ToLower(hex.EncodeToString(raw)), nil
}

func normalizeTronHexAddress(hexAddr string) string {
	hexAddr = strings.TrimSpace(hexAddr)
	hexAddr = strings.TrimPrefix(hexAddr, "0x")
	hexAddr = strings.TrimPrefix(hexAddr, "0X")
	return strings.ToLower(hexAddr)
}

type BlockHeader struct {
	RawData struct {
		Number         int64  `json:"number"`
		Timestamp      int64  `json:"timestamp"`
		WitnessAddress string `json:"witness_address"`
		ParentHash     string `json:"parentHash"`
		Version        int    `json:"version"`
	} `json:"raw_data"`
}

type TriggerSmartContractValue struct {
	OwnerAddress    string `json:"owner_address"`
	ContractAddress string `json:"contract_address"`
	Data            string `json:"data"`
	CallValue       int64  `json:"call_value"`
}

type ContractParam struct {
	TypeURL string          `json:"type_url"`
	Value   json.RawMessage `json:"value"`
}

type Transaction struct {
	TxID    string `json:"txID"`
	RawData struct {
		Contract []struct {
			Type      string        `json:"type"`
			Parameter ContractParam `json:"parameter"`
		} `json:"contract"`
		Timestamp int64 `json:"timestamp"`
		FeeLimit  int64 `json:"fee_limit"`
	} `json:"raw_data"`
	Ret []struct {
		ContractRet string `json:"contractRet"`
	} `json:"ret"`
}

type Block struct {
	BlockID      string        `json:"blockID"`
	BlockHeader  BlockHeader   `json:"block_header"`
	Transactions []Transaction `json:"transactions"`
}

type TRC20Transfer struct {
	TxID   string
	From   string
	To     string
	Raw    *big.Int
	Status string
	Token  mdb.ChainToken
}

type TRXTransfer struct {
	TxID   string
	From   string
	To     string
	RawSun int64 // 单位: SUN
	Status string
}

type TransferContractValue struct {
	OwnerAddress string `json:"owner_address"`
	ToAddress    string `json:"to_address"`
	Amount       int64  `json:"amount"` // 单位: SUN
}

var httpClient = &http.Client{Timeout: RequestTimeout}

func doPost(url string, apiKey string, body interface{}) ([]byte, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

func GetNowBlock(baseURL string, apiKey string) (*Block, error) {
	b, err := doPost(baseURL+"/wallet/getnowblock", apiKey, map[string]interface{}{})
	if err != nil {
		data.RecordRpcFailure(mdb.NetworkTron)
		return nil, err
	}
	var block Block
	if err := json.Unmarshal(b, &block); err != nil {
		data.RecordRpcFailure(mdb.NetworkTron)
		return nil, err
	}
	data.RecordRpcSuccess(mdb.NetworkTron)
	data.RecordRpcBlockHeight(mdb.NetworkTron, block.BlockHeader.RawData.Number)
	return &block, nil
}

func GetBlockByNum(baseURL string, apiKey string, num int64) (*Block, error) {
	b, err := doPost(baseURL+"/wallet/getblockbynum", apiKey, map[string]interface{}{"num": num})
	if err != nil {
		data.RecordRpcFailure(mdb.NetworkTron)
		return nil, err
	}
	var block Block
	if err := json.Unmarshal(b, &block); err != nil {
		data.RecordRpcFailure(mdb.NetworkTron)
		return nil, err
	}
	data.RecordRpcSuccess(mdb.NetworkTron)
	return &block, nil
}

func loadTronTRC20TokenMap() map[string]mdb.ChainToken {
	tokens, err := data.ListEnabledChainTokensByNetwork(mdb.NetworkTron)
	if err != nil {
		log.Sugar.Warnf("[TRON-BLOCK] load chain_tokens: %v", err)
		return nil
	}
	tokenMap := make(map[string]mdb.ChainToken)
	for _, token := range tokens {
		symbol := strings.ToUpper(strings.TrimSpace(token.Symbol))
		contractAddress := strings.TrimSpace(token.ContractAddress)
		if symbol == "TRX" || contractAddress == "" {
			continue
		}
		contractHex, err := TronAddressToHex(contractAddress)
		if err != nil {
			log.Sugar.Warnf("[TRON-BLOCK] skip invalid TRC20 contract symbol=%s address=%s err=%v", symbol, contractAddress, err)
			continue
		}
		tokenMap[contractHex] = token
	}
	return tokenMap
}

func parseTRC20Transfer(tx Transaction, tokenMap map[string]mdb.ChainToken) *TRC20Transfer {
	if len(tokenMap) == 0 {
		return nil
	}
	if len(tx.RawData.Contract) == 0 {
		return nil
	}
	c := tx.RawData.Contract[0]
	if c.Type != "TriggerSmartContract" {
		return nil
	}

	var val TriggerSmartContractValue
	if err := json.Unmarshal(c.Parameter.Value, &val); err != nil {
		return nil
	}

	contractHex := normalizeTronHexAddress(val.ContractAddress)
	token, ok := tokenMap[contractHex]
	if !ok {
		return nil
	}

	// 解析 data 字段
	// 格式: [4字节方法ID][32字节 to 地址][32字节 amount]
	data := strings.TrimPrefix(strings.ToLower(val.Data), "0x")
	if len(data) < 8+64+64 {
		return nil
	}
	if data[:8] != TransferMethodID {
		return nil
	}

	// to 地址：后 40 个十六进制字符（20 字节）
	toHex := data[8+24 : 8+64] // 跳过前12字节填充，取后20字节
	toAddr, err := HexToTronAddress(toHex)
	if err != nil {
		return nil
	}

	// amount：后 32 字节大整数
	amountHex := data[8+64 : 8+64+64]
	amountBytes, err := hex.DecodeString(amountHex)
	if err != nil {
		return nil
	}
	amountBig := new(big.Int).SetBytes(amountBytes)

	// from 地址
	fromAddr, err := HexToTronAddress(val.OwnerAddress)
	if err != nil {
		fromAddr = val.OwnerAddress
	}

	// 交易状态
	status := "SUCCESS"
	if len(tx.Ret) > 0 && tx.Ret[0].ContractRet != "" && tx.Ret[0].ContractRet != "SUCCESS" {
		status = tx.Ret[0].ContractRet
	}

	return &TRC20Transfer{
		TxID:   tx.TxID,
		From:   fromAddr,
		To:     toAddr,
		Raw:    amountBig,
		Status: status,
		Token:  token,
	}
}

func parseTRXTransfer(tx Transaction) *TRXTransfer {
	if len(tx.RawData.Contract) == 0 {
		return nil
	}
	c := tx.RawData.Contract[0]
	if c.Type != "TransferContract" {
		return nil
	}

	var val TransferContractValue
	if err := json.Unmarshal(c.Parameter.Value, &val); err != nil {
		return nil
	}
	if val.Amount <= 0 {
		return nil
	}

	fromAddr, err := HexToTronAddress(val.OwnerAddress)
	if err != nil {
		fromAddr = val.OwnerAddress
	}
	toAddr, err := HexToTronAddress(val.ToAddress)
	if err != nil {
		toAddr = val.ToAddress
	}

	status := "SUCCESS"
	if len(tx.Ret) > 0 && tx.Ret[0].ContractRet != "" && tx.Ret[0].ContractRet != "SUCCESS" {
		status = tx.Ret[0].ContractRet
	}

	return &TRXTransfer{
		TxID:   tx.TxID,
		From:   fromAddr,
		To:     toAddr,
		RawSun: val.Amount,
		Status: status,
	}
}

func processBlock(block *Block, tokenMap map[string]mdb.ChainToken) {
	blockTsMs := block.BlockHeader.RawData.Timestamp
	for _, tx := range block.Transactions {
		if t := parseTRC20Transfer(tx, tokenMap); t != nil {
			if t.Status != "SUCCESS" {
				continue
			}
			service.TryProcessTronTRC20Transfer(t.Token, t.To, t.Raw, t.TxID, blockTsMs)
			continue
		}
		if t := parseTRXTransfer(tx); t != nil {
			if t.Status != "SUCCESS" {
				continue
			}
			service.TryProcessTronTRXTransfer(t.To, t.RawSun, t.TxID, blockTsMs)
		}
	}
}

type Scanner struct {
	baseURL   string
	apiKey    string
	nodeID    uint64
	nodeLabel string
	lastBlock int64
	// 统计
	totalBlocks   int64
	totalTRC20Txs int64
	totalTRXTxs   int64
}

func NewScanner() *Scanner {
	return &Scanner{}
}

func (s *Scanner) Init() error {
	node, err := service.ResolveTronRpcNode()
	if err != nil {
		return fmt.Errorf("resolve tron node: %w", err)
	}
	s.useRpcNode(node)

	log.Sugar.Infof("[TRON-BLOCK] using RPC node %s", s.nodeLabel)
	block, err := GetNowBlock(s.baseURL, s.apiKey)
	if err != nil {
		s.recordRpcFailure("initial get latest block")
		return fmt.Errorf("获取初始块失败: %w", err)
	}
	s.lastBlock = block.BlockHeader.RawData.Number
	log.Sugar.Infof("[TRON-BLOCK] start block=%d", s.lastBlock)
	return nil
}

func (s *Scanner) useRpcNode(node *mdb.RpcNode) {
	s.baseURL = strings.TrimRight(strings.TrimSpace(node.Url), "/")
	s.apiKey = node.ApiKey
	s.nodeID = node.ID
	s.nodeLabel = data.RpcNodeLogLabel(*node)
}

func (s *Scanner) Run() {
	log.Sugar.Info("[TRON-BLOCK] start scanning (TRC20 + TRX)")
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	statTicker := time.NewTicker(60 * time.Second)
	defer statTicker.Stop()

	for {
		select {
		case <-statTicker.C:
			log.Sugar.Infof("[TRON-BLOCK] stats blocks=%d trc20=%d trx=%d", s.totalBlocks, s.totalTRC20Txs, s.totalTRXTxs)
		case <-ticker.C:
			s.poll()
		}
	}
}

func (s *Scanner) poll() {
	latest, err := GetNowBlock(s.baseURL, s.apiKey)
	if err != nil {
		log.Sugar.Warnf("[TRON-BLOCK] get latest block: %v", err)
		s.recordRpcFailure("get latest block")
		return
	}
	latestNum := latest.BlockHeader.RawData.Number
	if latestNum <= s.lastBlock {
		data.RecordRpcNodeSuccess(s.nodeID)
		return
	}

	tokenMap := loadTronTRC20TokenMap()
	hadBlockFetchError := false
	for num := s.lastBlock + 1; num <= latestNum; num++ {
		var block *Block
		if num == latestNum {
			block = latest
		} else {
			block, err = GetBlockByNum(s.baseURL, s.apiKey, num)
			if err != nil {
				log.Sugar.Warnf("[TRON-BLOCK] get block %d: %v", num, err)
				hadBlockFetchError = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		processBlock(block, tokenMap)
		s.lastBlock = num
		s.totalBlocks++

		for _, tx := range block.Transactions {
			if parseTRC20Transfer(tx, tokenMap) != nil {
				s.totalTRC20Txs++
			} else if parseTRXTransfer(tx) != nil {
				s.totalTRXTxs++
			}
		}
	}
	if hadBlockFetchError {
		s.recordRpcFailure("get historical block")
		return
	}
	data.RecordRpcNodeSuccess(s.nodeID)
}

func (s *Scanner) recordRpcFailure(reason string) {
	failures, cooling := data.RecordRpcNodeFailure(s.nodeID)
	if !cooling {
		log.Sugar.Warnf("[TRON-BLOCK] RPC node failed (%s), node=%s failures=%d/%d", reason, s.nodeLabel, failures, data.RpcFailoverThreshold)
		return
	}

	log.Sugar.Warnf("[TRON-BLOCK] RPC node reached fail threshold (%s), node=%s, trying another node", reason, s.nodeLabel)
	node, err := service.ResolveTronRpcNode(s.nodeID)
	if err != nil {
		log.Sugar.Warnf("[TRON-BLOCK] no alternate RPC node available after failure, keep current node=%s: %v", s.nodeLabel, err)
		return
	}
	oldLabel := s.nodeLabel
	s.useRpcNode(node)
	log.Sugar.Warnf("[TRON-BLOCK] switched RPC node from %s to %s", oldLabel, s.nodeLabel)
}

func StartTronBlockScannerListener() {
	for {
		scanner := NewScanner()
		if err := scanner.Init(); err != nil {
			log.Sugar.Errorf("[TRON-BLOCK] init: %v, retrying...", err)
			time.Sleep(10 * time.Second)
			continue
		}
		scanner.Run()
		log.Sugar.Warn("[TRON-BLOCK] scanner stopped, restarting...")
		time.Sleep(3 * time.Second)
	}
}
