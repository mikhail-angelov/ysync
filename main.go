package main

import (
	"context"
	"fmt"
	"os"

	"ysync/ysync"
)

func main() {
	cfg, err := ysync.ParseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cfg.ShowVersion {
		fmt.Println(ysync.Version)
		return
	}

	if err := ysync.Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
