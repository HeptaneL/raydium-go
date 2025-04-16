package config

import (
	"raydium-go/consts"

	"github.com/gagliardetto/solana-go"
)

var (
	Raydium_AMM_Program = map[string]solana.PublicKey{
		consts.MainNet: solana.MustPublicKeyFromBase58("675kPX9MHTjS2zt1qfr1NYHuzeLXfQM9H24wFSUt1Mp8"),
		consts.DevNet:  solana.MustPublicKeyFromBase58("HWy1jotHpo6UqeQxx49dpYYdQB8wj9Qk9MdxwjLvDHB8"),
	}
	Raydium_OpenBook_Program = map[string]solana.PublicKey{
		consts.MainNet: solana.MustPublicKeyFromBase58("srmqPvymJeFKQ4zGQed1GFppgkRHL9kaELCbyksJtPX"),
		consts.DevNet:  solana.MustPublicKeyFromBase58("EoTcMgcDRTJVZDMZWBoU6rhYHZfkNTVEAfz3uUJRcYGj"),
	}
)
