package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/blocto/solana-go-sdk/client"
	"github.com/blocto/solana-go-sdk/common"
	"github.com/blocto/solana-go-sdk/pkg/pointer"
	"github.com/blocto/solana-go-sdk/program/associated_token_account"
	"github.com/blocto/solana-go-sdk/program/metaplex/token_metadata"
	"github.com/blocto/solana-go-sdk/program/system"
	"github.com/blocto/solana-go-sdk/program/token"
	"github.com/blocto/solana-go-sdk/rpc"
	"github.com/blocto/solana-go-sdk/types"
	"github.com/davecgh/go-spew/spew"

	"github.com/tyler-smith/go-bip39"
)

type NftMintReq struct {
	receiver   common.PublicKey
	name       string
	uri        string
	collection common.PublicKey
}

type NftTransferReq struct {
	tokenAddress common.PublicKey
	sender       types.Account
	receiver     common.PublicKey
}

func mintNFT(c *client.Client, feePayer types.Account, req *NftMintReq) (txHash string, tokenPubkey *common.PublicKey, err error) {

	mint := types.NewAccount()

	ata, _, err := common.FindAssociatedTokenAddress(req.receiver, mint.PublicKey)
	if err != nil {
		slog.Error("failed to find a valid ata, err: ", "error", err)
		return "", nil, err
	}

	tokenMetadataPubkey, err := token_metadata.GetTokenMetaPubkey(mint.PublicKey)
	if err != nil {
		slog.Error("failed to find a valid token metadata, err: ", "error", err)
		return "", nil, err
	}
	tokenMasterEditionPubkey, err := token_metadata.GetMasterEdition(mint.PublicKey)
	if err != nil {
		slog.Error("failed to find a valid master edition, err: ", "error", err)
		return "", nil, err
	}

	mintAccountRent, err := c.GetMinimumBalanceForRentExemption(context.Background(), token.MintAccountSize)
	if err != nil {
		slog.Error("failed to get mint account rent, err: ", "error", err)
		return "", nil, err
	}

	recentBlockhashResponse, err := c.GetLatestBlockhashWithConfig(context.Background(), client.GetLatestBlockhashConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		slog.Error("failed to get recent blockhash, err: ", "error", err)
		return "", nil, err
	}

	tx, err := types.NewTransaction(types.NewTransactionParam{
		Signers: []types.Account{mint, feePayer},
		Message: types.NewMessage(types.NewMessageParam{
			FeePayer:        feePayer.PublicKey,
			RecentBlockhash: recentBlockhashResponse.Blockhash,
			Instructions: []types.Instruction{
				system.CreateAccount(system.CreateAccountParam{
					From:     feePayer.PublicKey,
					New:      mint.PublicKey,
					Owner:    common.TokenProgramID,
					Lamports: mintAccountRent,
					Space:    token.MintAccountSize,
				}),
				token.InitializeMint(token.InitializeMintParam{
					Decimals:   0,
					Mint:       mint.PublicKey,
					MintAuth:   feePayer.PublicKey,
					FreezeAuth: &feePayer.PublicKey,
				}),
				token_metadata.CreateMetadataAccountV3(token_metadata.CreateMetadataAccountV3Param{
					Metadata:                tokenMetadataPubkey,
					Mint:                    mint.PublicKey,
					MintAuthority:           feePayer.PublicKey,
					Payer:                   feePayer.PublicKey,
					UpdateAuthority:         feePayer.PublicKey,
					UpdateAuthorityIsSigner: true,
					IsMutable:               false,
					Data: token_metadata.DataV2{
						Name:                 req.name,
						Symbol:               "",
						Uri:                  req.uri,
						SellerFeeBasisPoints: 0,
						Creators:             nil,
						Collection: &token_metadata.Collection{
							Verified: false,
							Key:      req.collection,
						},
						Uses: nil,
					},
					CollectionDetails: nil,
				}),
				associated_token_account.CreateAssociatedTokenAccount(associated_token_account.CreateAssociatedTokenAccountParam{
					Funder:                 feePayer.PublicKey,
					Owner:                  req.receiver,
					Mint:                   mint.PublicKey,
					AssociatedTokenAccount: ata,
				}),
				token.MintTo(token.MintToParam{
					Mint:   mint.PublicKey,
					To:     ata,
					Auth:   feePayer.PublicKey,
					Amount: 1,
				}),
				token_metadata.CreateMasterEditionV3(token_metadata.CreateMasterEditionParam{
					Edition:         tokenMasterEditionPubkey,
					Mint:            mint.PublicKey,
					UpdateAuthority: feePayer.PublicKey,
					MintAuthority:   feePayer.PublicKey,
					Metadata:        tokenMetadataPubkey,
					Payer:           feePayer.PublicKey,
					MaxSupply:       pointer.Get[uint64](0),
				}),
			},
		}),
	})
	if err != nil {
		slog.Error("failed to new a tx, err: ", "error", err)
		return "", nil, err
	}

	txSig, err := c.SendTransactionWithConfig(context.Background(), tx, client.SendTransactionConfig{PreflightCommitment: rpc.CommitmentConfirmed})
	if err != nil {
		slog.Error("failed to send tx, err: ", "error", err)
		return "", nil, err
	}

	return txSig, &ata, nil

}

func transferNFT(c *client.Client, feePayer types.Account, req *NftTransferReq) (txHash string, tokenPubkey *common.PublicKey, err error) {

	//token account info
	tokenInfo, err := c.GetAccountInfoWithConfig(context.TODO(), req.tokenAddress.ToBase58(), client.GetAccountInfoConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		slog.Error("failed to get account info, err: ", "error", err)
		return "", nil, err
	}
	tokenAccount, err := token.TokenAccountFromData(tokenInfo.Data)
	if err != nil {
		slog.Error("failed to parse data to a token account, err: ", "error", err)
		return "", nil, err
	}
	mintPubkey := tokenAccount.Mint

	// Sender's ATA (must already exist)
	senderAta, _, err := common.FindAssociatedTokenAddress(req.sender.PublicKey, mintPubkey)
	if err != nil {
		slog.Error("failed to find sender's ATA: ", "error", err)
		return "", nil, err
	}

	// Recipient's ATA (may not exist yet)
	receiverAta, _, err := common.FindAssociatedTokenAddress(req.receiver, mintPubkey)
	if err != nil {
		slog.Error("failed to find recipient's ATA: ", "error", err)
		return "", nil, err
	}

	res, err := c.GetLatestBlockhashWithConfig(context.Background(), client.GetLatestBlockhashConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		slog.Error("get recent block hash error, err: ", "error", err)
		return "", nil, err
	}

	tx, err := types.NewTransaction(types.NewTransactionParam{
		Message: types.NewMessage(types.NewMessageParam{
			FeePayer:        feePayer.PublicKey,
			RecentBlockhash: res.Blockhash,
			Instructions: []types.Instruction{
				associated_token_account.CreateIdempotent(associated_token_account.CreateIdempotentParam{
					Funder:                 feePayer.PublicKey,
					Owner:                  req.receiver,
					Mint:                   mintPubkey,
					AssociatedTokenAccount: receiverAta,
				}),
				token.TransferChecked(token.TransferCheckedParam{
					From:     senderAta,
					To:       receiverAta,
					Mint:     mintPubkey,
					Auth:     req.sender.PublicKey,
					Signers:  []common.PublicKey{},
					Amount:   1,
					Decimals: 0,
				}),
			},
		}),
		Signers: []types.Account{feePayer, req.sender},
	})
	if err != nil {
		slog.Error("failed to new tx, err: ", "error", err)
		return "", nil, err
	}

	txSig, err := c.SendTransactionWithConfig(context.Background(), tx, client.SendTransactionConfig{PreflightCommitment: rpc.CommitmentConfirmed})
	if err != nil {
		slog.Error("send raw tx error, err: ", "error", err)
		return "", nil, err
	}

	return txSig, &receiverAta, nil
}

func waitForTxConfirmation(c *client.Client, txHash string) {
	// Wait for transaction confirmation ---
	fmt.Println("waiting for tx", txHash, "confirmation...")
	for {
		// Get the transaction status
		statuses, err := c.GetSignatureStatuses(context.Background(), []string{txHash})
		if err != nil {
			log.Printf("Failed to get signature statuses: %v", err)
			time.Sleep(2 * time.Second) // Wait before retrying
			continue
		}

		if len(statuses) > 0 && statuses[0] != nil {
			if *statuses[0].ConfirmationStatus == rpc.CommitmentConfirmed {
				fmt.Printf("Transaction successfully confirmed!\n\n")
				break
			} else {
				fmt.Println("Transaction is being processed...")
			}
		} else {
			fmt.Println("Transaction status not yet available...")
		}

		// Wait for a short period before polling again
		time.Sleep(2 * time.Second)
	}
}

func getNFTInfo(c *client.Client, ata common.PublicKey) {

	fmt.Println("token info for:", ata.ToBase58(), "-------------------------------------------")

	//token account info
	getAccountInfoResponse, err := c.GetAccountInfoWithConfig(context.TODO(), ata.ToBase58(), client.GetAccountInfoConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		log.Fatalf("failed to get account info, err: %v", err)
	}

	tokenAccount, err := token.TokenAccountFromData(getAccountInfoResponse.Data)
	if err != nil {
		log.Fatalf("failed to parse data to a token account, err: %v", err)
	}

	fmt.Printf("token account:\n%+v\n\n", tokenAccount)

	mint := tokenAccount.Mint

	//mint account info
	getAccountInfoResponse, err = c.GetAccountInfoWithConfig(context.TODO(), mint.ToBase58(), client.GetAccountInfoConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		log.Fatalf("failed to get account info, err: %v", err)
	}

	mintAccount, err := token.MintAccountFromData(getAccountInfoResponse.Data)
	if err != nil {
		log.Fatalf("failed to parse data to a mint account, err: %v", err)
	}

	fmt.Printf("mint account:\n%+v\n\n", mintAccount)

	//metadata account info
	metadataAccount, err := token_metadata.GetTokenMetaPubkey(mint)
	if err != nil {
		log.Fatalf("faield to get metadata account, err: %v", err)
	}

	// get data which stored in metadataAccount
	accountInfo, err := c.GetAccountInfoWithConfig(context.Background(), metadataAccount.ToBase58(), client.GetAccountInfoConfig{Commitment: rpc.CommitmentConfirmed})
	if err != nil {
		log.Fatalf("failed to get accountInfo, err: %v", err)
	}

	// parse it
	metadata, err := token_metadata.MetadataDeserialize(accountInfo.Data)
	if err != nil {
		log.Fatalf("failed to parse metaAccount, err: %v", err)
	}
	fmt.Println("metadata account:")
	spew.Dump(metadata)

	fmt.Println("---------------------------------------------------------------------")
}

func main() {

	mnemonic := "near industry doctor stool celery vehicle enlist symbol skate plastic ceiling zero"
	seed := bip39.NewSeed(mnemonic, "") // (mnemonic, password)
	feePayer, err := types.AccountFromSeed(seed[:32])
	if err != nil {
		log.Fatalf("failed to load feePayer account, err: %v", err)
	}
	fmt.Printf("feePayer: %v\n\n", feePayer.PublicKey.ToBase58())

	mnemonic = "manual still spice defense merry danger bus venture rare peace matrix federal"
	seed = bip39.NewSeed(mnemonic, "") // (mnemonic, password)
	user1, err := types.AccountFromSeed(seed[:32])
	if err != nil {
		log.Fatalf("failed to load user1 account, err: %v", err)
	}
	fmt.Printf("user1: %v\n\n", user1.PublicKey.ToBase58())

	c := client.NewClient(rpc.DevnetRPCEndpoint)

	//show feePayer balance
	balance, err := c.GetBalance(
		context.TODO(),
		feePayer.PublicKey.ToBase58(),
	)
	if err != nil {
		log.Fatalf("failed to request airdrop, err: %v", err)
	}
	fmt.Printf("feePayer balance: %v\n\n", balance)

	//show feePayer balance
	balance, err = c.GetBalance(
		context.TODO(),
		user1.PublicKey.ToBase58(),
	)
	if err != nil {
		log.Fatalf("failed to request airdrop, err: %v", err)
	}
	fmt.Printf("user1 balance: %v\n\n", balance)

	mint := types.NewAccount()
	fmt.Printf("NFT: %v\n\n", mint.PublicKey.ToBase58())

	collection := types.NewAccount()
	fmt.Printf("collection: %v\n\n", collection.PublicKey.ToBase58())

	receiver := types.NewAccount()
	fmt.Printf("receiver: %v\n\n", receiver.PublicKey.ToBase58())

	txHash, tokenAddress, err := mintNFT(c, feePayer, &NftMintReq{receiver: user1.PublicKey, name: "game nft 1", uri: "ipfs://123", collection: collection.PublicKey})
	if err != nil {
		return
	}
	waitForTxConfirmation(c, txHash)

	getNFTInfo(c, *tokenAddress)

	txHash, tokenAddress, err = transferNFT(c, feePayer, &NftTransferReq{tokenAddress: *tokenAddress, sender: user1, receiver: receiver.PublicKey})
	if err != nil {
		return
	}
	waitForTxConfirmation(c, txHash)

	getNFTInfo(c, *tokenAddress)

}
