package main

import (
	"fmt"
	"os"

	"NanoKVM-Server/authn"
)

// totpReset turns two-factor authentication off for one account, from the
// console: NanoKVM-Server totp-reset USER (the nkos-totp-reset command).
func totpReset(args []string) int {
	if len(args) != 1 || args[0] == "" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: nkos-totp-reset USER")
		fmt.Fprintln(os.Stderr, "Turns off two-factor authentication for a NanoKVM web account.")
		return 2
	}
	was, err := authn.DefaultStore.DisableTOTP(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "nkos-totp-reset: %s: %v\n", args[0], err)
		return 1
	}
	if was {
		fmt.Printf("Two-factor authentication is off for %s.\n", args[0])
	} else {
		fmt.Printf("%s does not use two-factor authentication.\n", args[0])
	}
	return 0
}
