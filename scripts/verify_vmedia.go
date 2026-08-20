//go:build ignore

// verify_vmedia mounts an ISO via AMI IUSB virtual media against a live BMC.
//
// Usage:
//
//	export OUTBAND_BMC_HOST=192.168.9.74
//	export OUTBAND_BMC_USER=root
//	export OUTBAND_BMC_PASS='...'
//	go run scripts/verify_vmedia.go -iso ./media/test.iso -duration 30s
//	go run scripts/verify_vmedia.go -url 'https://example.com/install.iso' -delivery auto
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"outband/internal/kvm"
)

func main() {
	host := envOr("OUTBAND_BMC_HOST", "192.168.9.74")
	user := envOr("OUTBAND_BMC_USER", "root")
	pass := os.Getenv("OUTBAND_BMC_PASS")
	iso := flag.String("iso", "", "path to ISO image")
	url := flag.String("url", "", "http(s) ISO URL (alternative to -iso)")
	delivery := flag.String("delivery", "auto", "URL delivery: auto, stream, or cache")
	duration := flag.Duration("duration", 60*time.Second, "how long to keep the redirection active")
	jnlpOnly := flag.Bool("jnlp", false, "print vmedia-related jnlp args and exit")
	flag.Parse()

	if pass == "" {
		log.Fatal("OUTBAND_BMC_PASS is required")
	}
	if !*jnlpOnly && *iso == "" && *url == "" {
		log.Fatal("-iso or -url is required (or pass -jnlp to inspect jnlp args)")
	}
	if !*jnlpOnly && *iso != "" && *url != "" {
		log.Fatal("pass only one of -iso or -url")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration+30*time.Second)
	defer cancel()

	args, cookie, err := kvm.FetchLaunchArgs(ctx, host, user, pass)
	if err != nil {
		log.Fatalf("jnlp: %v", err)
	}
	defer kvm.Logout(host, cookie)

	if *jnlpOnly {
		for _, k := range []string{"cdport", "fdport", "hdport", "vmsecure", "kvmport", "hostname"} {
			fmt.Printf("%s=%q\n", k, args[k])
		}
		fmt.Printf("kvmtoken_len=%d\n", len(args["kvmtoken"]))
		return
	}

	mgr := kvm.NewMediaManager(host, user, pass, "", time.Hour, nil)
	var mountErr error
	if *url != "" {
		req, err := kvm.ParseMountRequest("url", "", *url, *delivery)
		if err != nil {
			log.Fatalf("request: %v", err)
		}
		mountErr = mgr.MountRequest(ctx, req)
	} else {
		mountErr = mgr.Mount(ctx, *iso)
	}
	if mountErr != nil {
		log.Fatalf("mount: %v", mountErr)
	}
	defer mgr.Unmount()

	st := mgr.Status()
	log.Printf("mounted %q (%d bytes); holding for %s", st.ISO, st.Size, *duration)
	select {
	case <-ctx.Done():
	case <-time.After(*duration):
	}
	log.Printf("done")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
