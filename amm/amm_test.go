package amm

import (
	"context"
	"log"
	"raydium-go/config"
	"strconv"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

func TestSwap(t *testing.T) {
	network := "devnet" // or mainnet
	rpcUrl := "https://api.devnet.solana.com"
	poolAddress := "A73Z4EHWUaSrvL9AjFc22akNjenho2V2bYVafZNtSC5K"
	walletPath := "/Users/heptane/.config/solana/id.json"
	privateKey, err := solana.PrivateKeyFromSolanaKeygenFile(walletPath)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
		return
	}
	inputToken := "So11111111111111111111111111111111111111112"
	amount := uint64(1000000)
	baseIn := true
	client := rpc.New(rpcUrl)
	slippage := float64(0.1)
	res, err := Swap(client, network, poolAddress, inputToken, amount, baseIn, slippage, privateKey.String())
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(res)
}

func TestGetPoolState(t *testing.T) {
	rpcUrl := "https://api.devnet.solana.com"
	poolAddress := "A73Z4EHWUaSrvL9AjFc22akNjenho2V2bYVafZNtSC5K"
	pool, _ := solana.PublicKeyFromBase58(poolAddress)
	client := rpc.New(rpcUrl)
	resp, err := GetPoolState(client, pool)
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(resp)
	t.Log("swap fee numerator:", resp.Fees.SwapFeeNumerator)
	t.Log("swap fee denominator:", resp.Fees.SwapFeeDenominator)
	t.Log("pcVault: ", resp.PcVault)
	pcVaultAccount, err := client.GetTokenAccountBalance(context.Background(), resp.PcVault, rpc.CommitmentFinalized)
	pcVaultAmount, err := strconv.ParseUint(pcVaultAccount.Value.Amount, 10, 64)
	t.Log("pc amount:", pcVaultAmount)
	t.Log("coinVault: ", resp.CoinVault)
	coinVaultAccount, err := client.GetTokenAccountBalance(context.Background(), resp.CoinVault, rpc.CommitmentFinalized)
	coinVaultAmount, err := strconv.ParseUint(coinVaultAccount.Value.Amount, 10, 64)
	t.Log("coin amount:", coinVaultAmount)
	t.Log("NeedTakePnlPc: ", resp.StateData.NeedTakePnlPc)
	t.Log("NeedTakePnlCoin: ", resp.StateData.NeedTakePnlCoin)
	t.Log("total_pc_without_pnl:", pcVaultAmount-resp.StateData.NeedTakePnlPc)
	t.Log("total_coin_without_pnl:", coinVaultAmount-resp.StateData.NeedTakePnlCoin)
}

func TestGetMarketState(t *testing.T) {
	rpcUrl := "https://api.devnet.solana.com"
	marketAddress := "D5iPRhi6sEjbpanrbxGVvxp3voNR5fZ1jtMGWDX2qBbB"
	market, _ := solana.PublicKeyFromBase58(marketAddress)
	client := rpc.New(rpcUrl)
	resp, err := GetMarketState(client, market)
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(resp)
}

func TestGetAssociatedAuthority(t *testing.T) {
	program := config.Raydium_OpenBook_Program["devnet"]
	market, _ := solana.PublicKeyFromBase58("D5iPRhi6sEjbpanrbxGVvxp3voNR5fZ1jtMGWDX2qBbB")
	signer, nonce, err := GetAssociatedAuthority(program, market)
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(signer)
	t.Log(nonce)
}

func TestParseRayLog(t *testing.T) {
	msg := "ray_log: A0BCDwAAAAAAS8elcVACAAABAAAAAAAAAEBCDwAAAAAAziWk9e+IKAK1TovGDAAAAHDdYkWSAgAA"
	resp, err := ParseRayLog(msg)
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(resp)
}
