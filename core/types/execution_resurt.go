package types

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type ExecutionResurtCall struct {
	Type string `json:"type"`
	From common.Address `json:"from"`
	To common.Address `json:"to"`
	Value *hexutil.Big `json:"value"`
	Input hexutil.Bytes `json:"input"`
	Output hexutil.Bytes `json:"output"`
}

type ExecutionResurt struct {
	Calls []ExecutionResurtCall `json:"calls"`
}

type TraceTransactionOption struct {
	Tracer string `json:"tracer"`
}