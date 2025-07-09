package main

import (
	"context"
	"fmt"
	"log"
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

func mintNFT(c *client.Client, feePayer, mint, collection types.Account) {
	ata, _, err := common.FindAssociatedTokenAddress(feePayer.PublicKey, mint.PublicKey)
	if err != nil {
		log.Fatalf("failed to find a valid ata, err: %v", err)
	}

	tokenMetadataPubkey, err := token_metadata.GetTokenMetaPubkey(mint.PublicKey)
	if err != nil {
		log.Fatalf("failed to find a valid token metadata, err: %v", err)

	}
	tokenMasterEditionPubkey, err := token_metadata.GetMasterEdition(mint.PublicKey)
	if err != nil {
		log.Fatalf("failed to find a valid master edition, err: %v", err)
	}

	mintAccountRent, err := c.GetMinimumBalanceForRentExemption(context.Background(), token.MintAccountSize)
	if err != nil {
		log.Fatalf("failed to get mint account rent, err: %v", err)
	}

	recentBlockhashResponse, err := c.GetLatestBlockhash(context.Background())
	if err != nil {
		log.Fatalf("failed to get recent blockhash, err: %v", err)
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
						Name:                 "Fake SMS #1355",
						Symbol:               "",
						Uri:                  "https://34c7ef24f4v2aejh75xhxy5z6ars4xv47gpsdrei6fiowptk2nqq.arweave.net/3wXyF1wvK6ARJ_9ue-O58CMuXrz5nyHEiPFQ6z5q02E",
						SellerFeeBasisPoints: 0,
						Creators:             nil,
						Collection: &token_metadata.Collection{
							Verified: false,
							Key:      collection.PublicKey,
						},
						Uses: nil,
					},
					CollectionDetails: nil,
				}),
				associated_token_account.CreateAssociatedTokenAccount(associated_token_account.CreateAssociatedTokenAccountParam{
					Funder:                 feePayer.PublicKey,
					Owner:                  feePayer.PublicKey,
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
		log.Fatalf("failed to new a tx, err: %v", err)
	}

	txSig, err := c.SendTransaction(context.Background(), tx)
	if err != nil {
		log.Fatalf("failed to send tx, err: %v", err)
	}

	fmt.Printf("mint txid: %v\n\n", txSig)

	// Wait for transaction confirmation ---
	fmt.Println("waiting for tx confirmation...")
	for {
		// Get the transaction status
		statuses, err := c.GetSignatureStatuses(context.Background(), []string{txSig})
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

func transferNFT(c *client.Client, feePayer, mint, receiver types.Account) common.PublicKey {
	// Sender's ATA (must already exist)
	senderAta, _, err := common.FindAssociatedTokenAddress(feePayer.PublicKey, mint.PublicKey)
	if err != nil {
		panic(fmt.Errorf("failed to find sender's ATA: %w", err))
	}

	// Recipient's ATA (may not exist yet)
	receiverAta, _, err := common.FindAssociatedTokenAddress(receiver.PublicKey, mint.PublicKey)
	if err != nil {
		panic(fmt.Errorf("failed to find recipient's ATA: %w", err))
	}

	res, err := c.GetLatestBlockhash(context.Background())
	if err != nil {
		log.Fatalf("get recent block hash error, err: %v\n", err)
	}

	tx, err := types.NewTransaction(types.NewTransactionParam{
		Message: types.NewMessage(types.NewMessageParam{
			FeePayer:        feePayer.PublicKey,
			RecentBlockhash: res.Blockhash,
			Instructions: []types.Instruction{
				associated_token_account.CreateAssociatedTokenAccount(associated_token_account.CreateAssociatedTokenAccountParam{
					Funder:                 feePayer.PublicKey,
					Owner:                  receiver.PublicKey,
					Mint:                   mint.PublicKey,
					AssociatedTokenAccount: receiverAta,
				}),
				token.TransferChecked(token.TransferCheckedParam{
					From:     senderAta,
					To:       receiverAta,
					Mint:     mint.PublicKey,
					Auth:     feePayer.PublicKey,
					Signers:  []common.PublicKey{},
					Amount:   1,
					Decimals: 0,
				}),
			},
		}),
		Signers: []types.Account{feePayer},
	})
	if err != nil {
		log.Fatalf("failed to new tx, err: %v", err)
	}

	txSig, err := c.SendTransactionWithConfig(context.Background(), tx, client.SendTransactionConfig{PreflightCommitment: rpc.CommitmentConfirmed})
	if err != nil {
		log.Fatalf("send raw tx error, err: %v\n", err)
	}

	fmt.Printf("transfer txid: %v\n\n", txSig)

	// Wait for transaction confirmation ---
	fmt.Println("waiting for tx confirmation...")
	for {
		// Get the transaction status
		statuses, err := c.GetSignatureStatuses(context.Background(), []string{txSig})
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

	return receiverAta
}

func getNFTInfo(c *client.Client, ata common.PublicKey) {

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

}

func main() {

	mnemonic := "near industry doctor stool celery vehicle enlist symbol skate plastic ceiling zero"
	seed := bip39.NewSeed(mnemonic, "") // (mnemonic, password)
	feePayer, err := types.AccountFromSeed(seed[:32])
	if err != nil {
		log.Fatalf("failed to load feePayer account, err: %v", err)
	}
	fmt.Printf("feePayer: %v\n\n", feePayer.PublicKey.ToBase58())

	c := client.NewClient(rpc.TestnetRPCEndpoint)

	//show feePayer balance
	balance, err := c.GetBalance(
		context.TODO(),
		feePayer.PublicKey.ToBase58(),
	)
	if err != nil {
		log.Fatalf("failed to request airdrop, err: %v", err)
	}
	fmt.Printf("balance: %v\n\n", balance)

	mint := types.NewAccount()
	fmt.Printf("NFT: %v\n\n", mint.PublicKey.ToBase58())

	collection := types.NewAccount()
	fmt.Printf("collection: %v\n\n", collection.PublicKey.ToBase58())

	receiver := types.NewAccount()
	fmt.Printf("receiver: %v\n\n", receiver.PublicKey.ToBase58())

	mintNFT(c, feePayer, mint, collection)

	ata := transferNFT(c, feePayer, mint, receiver)

	getNFTInfo(c, ata)

}
