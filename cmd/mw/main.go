package main

import (
"log"
"os"

"myworktree/internal/cli"
)

func main() {
logger := log.New(os.Stderr, "", log.LstdFlags)
os.Exit(cli.Run(os.Args, logger))
}
