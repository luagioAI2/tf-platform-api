package main

import (
	"context"
	"log"
	"os"

	app "tf-api/internal/app"

	"github.com/urfave/cli/v2"
)

// VERSION 版本号，通过编译参数注入：-X main.VERSION=x.x.x
var VERSION = "dev"

//	@title						tf-api
//	@version					1.0.0
//	@description				tf-api 服务端接口
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						Authorization
//	@schemes					http https
//	@BasePath					/

func main() {
	ctx := context.Background()

	cliApp := cli.NewApp()
	cliApp.Name = "tf-api"
	cliApp.Version = VERSION
	cliApp.Usage = "tf-api http server"
	cliApp.Commands = []*cli.Command{
		newWebCmd(ctx),
	}

	if err := cliApp.Run(os.Args); err != nil {
		log.Fatalf("application error: %v", err)
	}
}

func newWebCmd(ctx context.Context) *cli.Command {
	return &cli.Command{
		Name:  "web",
		Usage: "run http server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "conf",
				Aliases: []string{"c"},
				Usage:   "config file path, e.g. configs/config.yaml",
				Value:   "configs/config.yaml",
			},
		},
		Action: func(c *cli.Context) error {
			return app.Run(ctx,
				app.SetConfigFile(c.String("conf")),
				app.SetVersion(VERSION))
		},
	}
}
