//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"mipmi/internal/amiweb"
)

func main() {
	host := "192.168.9.74"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args, cookie, err := amiweb.FetchLaunchArgs(ctx, host, "root", os.Getenv("MIPMI_BMC_PASS"))
	if err != nil {
		panic(err)
	}
	fmt.Printf("cookie=%q\n", cookie)
	for k, v := range args {
		fmt.Printf("%s=%q hex=%x\n", k, v, v)
	}
	amiweb.Logout(host, cookie)
}
