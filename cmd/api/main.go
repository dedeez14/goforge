// Command api is the HTTP entrypoint for the goforge service.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dedeez14/goforge/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
