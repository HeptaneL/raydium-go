package amm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"raydium-go/config"
	"strconv"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	computebudget "github.com/gagliardetto/solana-go/programs/compute-budget"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

var (
	computeUnitLimit               = uint32(68000)
	priorityFee                    = uint64(100)
	dataSize                       = uint64(165)
	WSOL                           = solana.MustPublicKeyFromBase58("So11111111111111111111111111111111111111112")
	PC2Coin          SwapDirection = "pc2coin"
	Coin2PC          SwapDirection = "coin2Pc"
)

type SwapDirection string

type SwapInstructionBaseIn struct {
	// SOURCE amount to transfer, output to DESTINATION is based on the exchange rate
	AmountIn uint64
	/// Minimum amount of DESTINATION token to output, prevents excessive slippage
	MinimumAmountOut uint64
}

type SwapInstructionBaseOut struct {
	// SOURCE amount to transfer, output to DESTINATION is based on the exchange rate
	MaxAmountIn uint64
	/// Minimum amount of DESTINATION token to output, prevents excessive slippage
	AmountOut uint64
}

type SwapBaseInLog struct {
	LogType    uint8
	AmountIn   uint64
	MinimumOut uint64
	Direction  uint64
	UserSource uint64
	PoolCoin   uint64
	PoolPc     uint64
	OutAmount  uint64
}

type SwapBaseOutLog struct {
	LogType    uint8
	MaxIn      uint64
	AmountOut  uint64
	Direction  uint64
	UserSource uint64
	PoolCoin   uint64
	PoolPc     uint64
	DeductIn   uint64
}

// 枚举日志类型
const (
	LogInit        = 0
	LogDeposit     = 1
	LogWithdraw    = 2
	LogSwapBaseIn  = 3
	LogSwapBaseOut = 4
)

func Swap(client *rpc.Client, network string, poolAddress string, inputTokenAddress string, amountSpecified uint64, baseIn bool, slippage float64, privateKey string) (string, error) {
	pool, err := solana.PublicKeyFromBase58(poolAddress)
	if err != nil {
		return "", err
	}
	poolState, err := GetPoolState(client, pool)
	if err != nil {
		return "", err
	}
	marketState, err := GetMarketState(client, poolState.Market)
	if err != nil {
		return "", err
	}
	signer, err := solana.PrivateKeyFromBase58(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	payerPubKey := signer.PublicKey()
	var inputMint solana.PublicKey
	var outputMint solana.PublicKey
	var direction SwapDirection
	if inputTokenAddress == poolState.CoinVaultMint.String() {
		inputMint = poolState.CoinVaultMint
		outputMint = poolState.PcVaultMint
		direction = Coin2PC
	} else {
		inputMint = poolState.PcVaultMint
		outputMint = poolState.CoinVaultMint
		direction = PC2Coin
	}
	fmt.Println("inputMint:", inputMint)
	fmt.Println("outputMint:", outputMint)
	var instructions []solana.Instruction
	inputAta, inputAtaCreateInstruction, err := getOrCreateTokenAccountInstruction(client, inputMint, signer, amountSpecified, true)
	if err != nil {
		return "", fmt.Errorf("failed to find associated token address: %v", err)
	}
	if inputAtaCreateInstruction != nil {
		for _, inst := range inputAtaCreateInstruction {
			instructions = append(instructions, inst)
		}
	}

	outputAta, outputAtaCreateInstruction, err := getOrCreateTokenAccountInstruction(client, outputMint, signer, amountSpecified, false)
	if err != nil {
		return "", err
	}
	if outputAtaCreateInstruction != nil {
		for _, inst := range outputAtaCreateInstruction {
			instructions = append(instructions, inst)
		}
	}

	ammAuthority, _, _ := solana.FindProgramAddress([][]byte{{97, 109, 109, 32, 97, 117, 116, 104, 111, 114, 105, 116, 121}}, config.Raydium_AMM_Program[network])
	vaultSigner, _, err := GetAssociatedAuthority(poolState.MarketProgram, poolState.Market)
	pcVaultAccount, err := client.GetTokenAccountBalance(context.Background(), poolState.PcVault, rpc.CommitmentFinalized)
	pcVaultBalance, err := strconv.ParseUint(pcVaultAccount.Value.Amount, 10, 64)
	coinVaultAccount, err := client.GetTokenAccountBalance(context.Background(), poolState.CoinVault, rpc.CommitmentFinalized)
	coinVaultBalance, err := strconv.ParseUint(coinVaultAccount.Value.Amount, 10, 64)
	pcVaultAmount := pcVaultBalance - poolState.StateData.NeedTakePnlPc
	coinVaultAmount := coinVaultBalance - poolState.StateData.NeedTakePnlCoin
	var data []byte
	if baseIn {
		swapFee := uint64(float64(amountSpecified) * float64(poolState.Fees.SwapFeeNumerator) / float64(poolState.Fees.SwapFeeDenominator))
		swapInAfterDeductFee := amountSpecified - swapFee
		var minAmountOut uint64
		if direction == Coin2PC {
			minAmountOut = uint64(float64(pcVaultAmount) * float64(swapInAfterDeductFee) / float64(coinVaultAmount+swapInAfterDeductFee) * float64(1-slippage))
		} else {
			minAmountOut = uint64(float64(coinVaultAmount) * float64(swapInAfterDeductFee) / float64(pcVaultAmount+swapInAfterDeductFee) * float64(1-slippage))
		}
		data, err = baseInDataFrom(amountSpecified, minAmountOut)
		if err != nil {
			return "", err
		}
	} else {
		var maxAmountIn uint64
		if direction == Coin2PC {
			maxAmountIn = uint64(float64(coinVaultAmount) * float64(amountSpecified) / float64(pcVaultAmount-amountSpecified) * float64(1-float64(poolState.Fees.MinSeparateDenominator)/float64(poolState.Fees.SwapFeeNumerator)) * float64(1+slippage))
		} else {
			maxAmountIn = uint64(float64(pcVaultAmount) * float64(amountSpecified) / float64(coinVaultAmount-amountSpecified) * float64(1-float64(poolState.Fees.MinSeparateDenominator)/float64(poolState.Fees.SwapFeeNumerator)) * float64(1+slippage))
		}
		data, err = baseOutDataFrom(maxAmountIn, amountSpecified)
		if err != nil {
			return "", err
		}
	}
	swapInstruction := solana.NewInstruction(
		config.Raydium_AMM_Program[network],
		swapAccountsFrom(pool, ammAuthority, poolState.OpenOrders, poolState.TargetOrders, poolState.CoinVault, poolState.PcVault, poolState.MarketProgram, poolState.Market, marketState.Bids, marketState.Asks, marketState.EventQueue, marketState.BaseVault, marketState.QuoteVault, vaultSigner, inputAta, outputAta, payerPubKey),
		data,
	)
	instructions = append(instructions, swapInstruction)
	for _, a := range swapInstruction.AccountValues {
		fmt.Println(a.PublicKey.String())
	}
	if inputMint.Equals(WSOL) {
		closeAccInst, err := token.NewCloseAccountInstruction(
			inputAta,
			signer.PublicKey(),
			signer.PublicKey(),
			[]solana.PublicKey{},
		).ValidateAndBuild()

		if err != nil {
			return "", err
		}
		instructions = append(instructions, closeAccInst)
	} else if outputMint.Equals(WSOL) {
		closeAccInst, err := token.NewCloseAccountInstruction(
			outputAta,
			signer.PublicKey(),
			signer.PublicKey(),
			[]solana.PublicKey{},
		).ValidateAndBuild()

		if err != nil {
			return "", err
		}
		instructions = append(instructions, closeAccInst)
	}

	blockhash, err := client.GetLatestBlockhash(context.Background(), rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("failed to fetch recent blockhash: %w", err)
	}
	tx, err := solana.NewTransaction(
		swapInstructionsFrom(computeUnitLimit, priorityFee, instructions),
		blockhash.Value.Blockhash,
		solana.TransactionPayer(payerPubKey),
	)
	_, err = tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if signer.PublicKey().Equals(key) {
			return &signer
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}
	txHash, err := client.SendTransaction(context.Background(), tx)
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return txHash.String(), nil
}

func baseInDataFrom(amountIn uint64, minAmountOut uint64) ([]byte, error) {
	methodBytes, err := hex.DecodeString("09")
	if err != nil {
		return nil, err
	}
	params := new(bytes.Buffer)
	baseIn := SwapInstructionBaseIn{
		AmountIn:         amountIn,
		MinimumAmountOut: minAmountOut,
	}
	err = bin.NewBorshEncoder(params).Encode(&baseIn)
	if err != nil {
		return nil, err
	}
	data := append(methodBytes, params.Bytes()...)
	return data, nil
}

func baseOutDataFrom(maxAmountIn uint64, amountOut uint64) ([]byte, error) {
	methodBytes, err := hex.DecodeString("0b")
	if err != nil {
		return nil, err
	}
	params := new(bytes.Buffer)
	baseOut := SwapInstructionBaseOut{
		MaxAmountIn: maxAmountIn,
		AmountOut:   amountOut,
	}
	err = bin.NewBorshEncoder(params).Encode(&baseOut)
	if err != nil {
		return nil, err
	}
	data := append(methodBytes, params.Bytes()...)
	return data, nil
}

func swapInstructionsFrom(
	computeUnitLimit uint32,
	priorityFee uint64,
	insts []solana.Instruction,
) []solana.Instruction {
	computeUnitLimitInstruction := computebudget.NewSetComputeUnitLimitInstruction(
		computeUnitLimit,
	).Build()
	computeUnitPriceInstruction := computebudget.NewSetComputeUnitPriceInstruction(
		priorityFee,
	).Build()

	instructions := []solana.Instruction{computeUnitLimitInstruction, computeUnitPriceInstruction}
	for _, inst := range insts {
		instructions = append(instructions, inst)
	}
	return instructions
}

func swapAccountsFrom(
	pool solana.PublicKey,
	ammAuthority solana.PublicKey,
	openOrders solana.PublicKey,
	targetOrders solana.PublicKey,
	coinVault solana.PublicKey,
	pcVault solana.PublicKey,
	marketProgram solana.PublicKey,
	market solana.PublicKey,
	bids solana.PublicKey,
	asks solana.PublicKey,
	eventQueue solana.PublicKey,
	baseVault solana.PublicKey,
	quoteVault solana.PublicKey,
	vaultSigner solana.PublicKey,
	inputMint solana.PublicKey,
	outputMint solana.PublicKey,
	payer solana.PublicKey,
) []*solana.AccountMeta {
	return []*solana.AccountMeta{
		solana.NewAccountMeta(token.ProgramID, false, false), // TOKEN PROGRAM
		solana.NewAccountMeta(pool, true, false),             // AMM
		solana.NewAccountMeta(ammAuthority, false, false),    // AMM Authority
		solana.NewAccountMeta(openOrders, true, false),       // Amm Open Orders
		solana.NewAccountMeta(targetOrders, true, false),     // Amm Target Orders
		solana.NewAccountMeta(coinVault, true, false),        // Pool Coin Token Account
		solana.NewAccountMeta(pcVault, true, false),          // Pool Pc Token Account
		solana.NewAccountMeta(marketProgram, false, false),   // Serum PROGRAM
		solana.NewAccountMeta(market, true, false),           // Serum Market
		solana.NewAccountMeta(bids, true, false),             // Serum Bids
		solana.NewAccountMeta(asks, true, false),             // Serum Asks
		solana.NewAccountMeta(eventQueue, true, false),       // Serum Event Queue
		solana.NewAccountMeta(baseVault, true, false),        // Serum Coin Vault Account
		solana.NewAccountMeta(quoteVault, true, false),       // Serum Pc Vault Account
		solana.NewAccountMeta(vaultSigner, false, false),     // Serum Vault Signer
		solana.NewAccountMeta(inputMint, true, false),        // User Source Token Account
		solana.NewAccountMeta(outputMint, true, false),       // User Destination Token Account
		solana.NewAccountMeta(payer, true, true),             // User Source Owner
	}
}

func GetAssociatedAuthority(programID solana.PublicKey, marketID solana.PublicKey) (solana.PublicKey, uint8, error) {
	seeds := [][]byte{marketID.Bytes()}
	var nonce uint8 = 0

	for nonce < 100 {
		seedsWithNonce := append(seeds, int8ToBuf(nonce))
		seedsWithNonce = append(seedsWithNonce, make([]byte, 7)) // Buffer.alloc(7)

		publicKey, err := solana.CreateProgramAddress(seedsWithNonce, programID)
		if err != nil {
			nonce++
			continue
		}

		return publicKey, nonce, nil
	}

	return solana.PublicKey{}, 0, errors.New("unable to find a viable program address nonce")
}

func int8ToBuf(value uint8) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, value)
	return buf.Bytes()
}

func getOrCreateTokenAccountInstruction(client *rpc.Client, tokenMintPubKey solana.PublicKey, ownerPrivateKey solana.PrivateKey, amountSpecified uint64, input bool) (solana.PublicKey, []solana.Instruction, error) {
	var res []solana.Instruction
	owner := ownerPrivateKey.PublicKey()

	if tokenMintPubKey.Equals(WSOL) {
		accountLamport, err := client.GetMinimumBalanceForRentExemption(context.Background(), dataSize, rpc.CommitmentConfirmed)
		if err != nil {
			return solana.PublicKey{}, res, err
		}
		if input {
			accountLamport += amountSpecified
		}
		seed := solana.NewWallet().PublicKey().String()[0:32]
		publicKey, err := solana.CreateWithSeed(owner, seed, token.ProgramID)
		if err != nil {
			return solana.PublicKey{}, res, err
		}

		createInst, err := system.NewCreateAccountWithSeedInstruction(
			owner,
			seed,
			accountLamport,
			dataSize,
			token.ProgramID,
			owner,
			publicKey,
			owner,
		).ValidateAndBuild()

		if err != nil {
			return solana.PublicKey{}, res, err
		}
		res = append(res, createInst)

		initInst, err := token.NewInitializeAccountInstruction(
			publicKey,
			WSOL,
			owner,
			solana.SysVarRentPubkey,
		).ValidateAndBuild()
		res = append(res, initInst)
		return publicKey, res, nil
	}
	// Find the associated token account address
	ata, _, err := solana.FindAssociatedTokenAddress(owner, tokenMintPubKey)
	if err != nil {
		return solana.PublicKey{}, nil, fmt.Errorf("failed to find associated token address: %v", err)
	}

	// Check if the account already exists
	account, err := client.GetAccountInfo(context.TODO(), ata)
	if err == nil && account != nil {
		return ata, nil, nil // Account already exists, so no transaction signature
	}

	// Create the instruction to create the associated token account
	createATAIx := associatedtokenaccount.NewCreateInstruction(
		owner,           // payer
		owner,           // wallet owner
		tokenMintPubKey, // token mint
	).Build()
	res = append(res, createATAIx)
	return ata, res, nil
}

// Fees 对应 Rust 中的 Fees
type Fees struct {
	MinSeparateNumerator   uint64 `bin:""`
	MinSeparateDenominator uint64 `bin:""`
	TradeFeeNumerator      uint64 `bin:""`
	TradeFeeDenominator    uint64 `bin:""`
	PnlNumerator           uint64 `bin:""`
	PnlDenominator         uint64 `bin:""`
	SwapFeeNumerator       uint64 `bin:""`
	SwapFeeDenominator     uint64 `bin:""`
}

// StateData 对应 Rust 中的 StateData
type StateData struct {
	NeedTakePnlCoin     uint64      `bin:""`
	NeedTakePnlPc       uint64      `bin:""`
	TotalPnlPc          uint64      `bin:""`
	TotalPnlCoin        uint64      `bin:""`
	PoolOpenTime        uint64      `bin:""`
	Padding             [2]uint64   `bin:""`
	OrderbookToInitTime uint64      `bin:""`
	SwapCoinInAmount    bin.Uint128 `bin:""` // 使用 binary.Uint128
	SwapPcOutAmount     bin.Uint128 `bin:""`
	SwapAccPcFee        uint64      `bin:""`
	SwapPcInAmount      bin.Uint128 `bin:""`
	SwapCoinOutAmount   bin.Uint128 `bin:""`
	SwapAccCoinFee      uint64      `bin:""`
}

// AmmInfo 对应 Rust 中的 AmmInfo
type AmmInfo struct {
	Status             uint64           `bin:""`
	Nonce              uint64           `bin:""`
	OrderNum           uint64           `bin:""`
	Depth              uint64           `bin:""`
	CoinDecimals       uint64           `bin:""`
	PcDecimals         uint64           `bin:""`
	State              uint64           `bin:""`
	ResetFlag          uint64           `bin:""`
	MinSize            uint64           `bin:""`
	VolMaxCutRatio     uint64           `bin:""`
	AmountWave         uint64           `bin:""`
	CoinLotSize        uint64           `bin:""`
	PcLotSize          uint64           `bin:""`
	MinPriceMultiplier uint64           `bin:""`
	MaxPriceMultiplier uint64           `bin:""`
	SysDecimalValue    uint64           `bin:""`
	Fees               Fees             `bin:""`
	StateData          StateData        `bin:""`
	CoinVault          solana.PublicKey `bin:""`
	PcVault            solana.PublicKey `bin:""`
	CoinVaultMint      solana.PublicKey `bin:""`
	PcVaultMint        solana.PublicKey `bin:""`
	LpMint             solana.PublicKey `bin:""`
	OpenOrders         solana.PublicKey `bin:""`
	Market             solana.PublicKey `bin:""`
	MarketProgram      solana.PublicKey `bin:""`
	TargetOrders       solana.PublicKey `bin:""`
	Padding1           [8]uint64        `bin:""`
	AmmOwner           solana.PublicKey `bin:""`
	LpAmount           uint64           `bin:""`
	ClientOrderId      uint64           `bin:""`
	RecentEpoch        uint64           `bin:""`
	Padding2           uint64           `bin:""`
}

func GetPoolState(client *rpc.Client, pool solana.PublicKey) (AmmInfo, error) {
	var ammInfo AmmInfo
	err := client.GetAccountDataInto(context.TODO(), pool, &ammInfo)
	if err != nil {
		return ammInfo, err
	}
	return ammInfo, nil
}

type MarketState struct {
	AccountFlag            [5]byte
	Padding                [8]byte
	OwnAddress             solana.PublicKey
	VaultSignerNonce       uint64
	BaseMint               solana.PublicKey
	QuoteMint              solana.PublicKey
	BaseVault              solana.PublicKey
	BaseDepositsTotal      uint64
	BaseFeesAccrued        uint64
	QuoteVault             solana.PublicKey
	QuoteDepositsTotal     uint64
	QuoteFeesAccrued       uint64
	QuoteDustThreshold     uint64
	RequestQueue           solana.PublicKey
	EventQueue             solana.PublicKey
	Bids                   solana.PublicKey
	Asks                   solana.PublicKey
	BaseLotSize            uint64
	QuoteLotSize           uint64
	FeeRateBps             uint64
	ReferrerRebatesAccrued uint64
	PaddingEnd             [7]byte
}

func GetMarketState(client *rpc.Client, market solana.PublicKey) (MarketState, error) {
	var state MarketState
	err := client.GetAccountDataInto(context.Background(), market, &state)
	return state, err
}

func ParseRayLog(msg string) (interface{}, error) {
	// 去掉前缀
	base64Part := msg
	if len(msg) > 9 && msg[:9] == "ray_log: " {
		base64Part = msg[9:]
	}

	// 解码
	data, err := base64.StdEncoding.DecodeString(base64Part)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	if len(data) < 1 {
		return nil, fmt.Errorf("invalid log: no data")
	}

	logType := data[0]
	reader := bytes.NewReader(data)

	switch logType {
	case LogSwapBaseIn:
		var log SwapBaseInLog
		if err := binary.Read(reader, binary.LittleEndian, &log); err != nil {
			return nil, err
		}
		return log, nil
	case LogSwapBaseOut:
		var log SwapBaseOutLog
		if err := binary.Read(reader, binary.LittleEndian, &log); err != nil {
			return nil, err
		}
		return log, nil
	default:
		return nil, fmt.Errorf("unsupported log type: %d", logType)
	}
}
