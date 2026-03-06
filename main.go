package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lgbarn/kdiag/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, cmd.ErrHealthCritical) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
