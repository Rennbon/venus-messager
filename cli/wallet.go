package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/filecoin-project/venus-messager/models/repo"

	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/venus-messager/types"
)

var WalletCmds = &cli.Command{
	Name:  "wallet",
	Usage: "wallet commands",
	Subcommands: []*cli.Command{
		addWalletCmd,
		searchWalletCmd,
		listWalletCmd,
		listRemoteWalletAddrCmd,
		deleteWalletCmd,
	},
}

var addWalletCmd = &cli.Command{
	Name:  "add",
	Usage: "add a new wallet",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "name",
			Usage: "wallet name",
		},
		&cli.StringFlag{
			Name:  "url",
			Usage: "wallet url",
		},
		&cli.StringFlag{
			Name:  "token",
			Usage: "wallet token",
		},
	},
	Action: func(ctx *cli.Context) error {
		client, closer, err := getAPI(ctx)
		if err != nil {
			return err
		}
		defer closer()

		var w types.Wallet
		w.CreatedAt = time.Now()
		w.ID = types.NewUUID()
		w.State = types.Alive
		w.IsDeleted = repo.NotDeleted
		w.Name = ctx.String("name")
		w.Url = ctx.String("url")
		if len(w.Url) == 0 {
			return xerrors.Errorf("url cannot be empty")
		}
		w.Token = ctx.String("token")
		if len(w.Token) == 0 {
			return xerrors.Errorf("token cannot be empty")
		}

		_, err = client.SaveWallet(ctx.Context, &w)
		if err != nil {
			return err
		}

		return nil
	},
}

var searchWalletCmd = &cli.Command{
	Name:  "search",
	Usage: "search wallet by wallet id or wallet name, eg. venus-messager wallet search --name=xxxxx or --id=xxxx",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "id",
			Usage: "Search data according to wallet id",
		},
		&cli.StringFlag{
			Name:  "name",
			Usage: "Search data according to wallet name",
		},
	},
	Action: func(ctx *cli.Context) error {
		client, closer, err := getAPI(ctx)
		if err != nil {
			return err
		}
		defer closer()

		var uuid types.UUID
		var wallet *types.Wallet
		uuidStr := ctx.String("id")
		if len(uuidStr) > 0 {
			if uuid, err = types.ParseUUID(uuidStr); err != nil {
				return err
			}
			if wallet, err = client.GetWalletByID(ctx.Context, uuid); err != nil {
				return err
			}
		} else if name := ctx.String("name"); len(name) > 0 {
			if wallet, err = client.GetWalletByName(ctx.Context, name); err != nil {
				return err
			}
		} else {
			return xerrors.Errorf("must pass id or name")
		}

		bytes, err := json.MarshalIndent(transformWallet(wallet), " ", "\t")
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
		return nil
	},
}

var listWalletCmd = &cli.Command{
	Name:  "list",
	Usage: "list wallet",
	Action: func(ctx *cli.Context) error {
		client, closer, err := getAPI(ctx)
		if err != nil {
			return err
		}
		defer closer()

		wallets, err := client.ListWallet(ctx.Context)
		if err != nil {
			return err
		}
		ws := make([]*walletFormat, len(wallets))
		for i, w := range wallets {
			ws[i] = transformWallet(w)
		}

		bytes, err := json.MarshalIndent(ws, " ", "\t")
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
		return nil
	},
}

var listRemoteWalletAddrCmd = &cli.Command{
	Name:      "list-addr",
	Usage:     "list remote wallet address",
	ArgsUsage: "wallet_name",
	Aliases:   []string{"name"},
	Action: func(ctx *cli.Context) error {
		client, closer, err := getAPI(ctx)
		if err != nil {
			return err
		}
		defer closer()

		if !ctx.Args().Present() {
			return xerrors.Errorf("must pass name")
		}

		addrs, err := client.ListRemoteWalletAddress(ctx.Context, ctx.Args().First())
		if err != nil {
			return err
		}

		bytes, err := json.MarshalIndent(addrs, " ", "\t")
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
		return nil
	},
}

var deleteWalletCmd = &cli.Command{
	Name:      "del",
	Usage:     "delete wallet by name",
	ArgsUsage: "name",
	Action: func(ctx *cli.Context) error {
		client, closer, err := getAPI(ctx)
		if err != nil {
			return err
		}
		defer closer()

		if !ctx.Args().Present() {
			return xerrors.Errorf("must pass name")
		}
		name := ctx.Args().First()

		_, err = client.DeleteWallet(ctx.Context, name)
		if err != nil {
			return err
		}

		return nil
	},
}

type walletFormat struct {
	ID    types.UUID
	Name  string
	Url   string
	Token string
	State string

	IsDeleted int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func transformWallet(w *types.Wallet) *walletFormat {
	if w == nil {
		return nil
	}

	return &walletFormat{
		ID:        w.ID,
		Name:      w.Name,
		Url:       w.Url,
		Token:     w.Token,
		State:     types.StateToString(w.State),
		IsDeleted: w.IsDeleted,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
}
